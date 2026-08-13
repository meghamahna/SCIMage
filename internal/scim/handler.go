package scim

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/meghamahna/SCIMage/internal/store"
)

const (
	// A SCIM User is a few hundred bytes.
	maxBodyBytes = 1 << 20

	// user_name is indexed and a btree entry caps out around 2704 bytes, so an
	// unbounded attribute is a 500 waiting to happen.
	maxAttrLen = 256
)

type Handler struct {
	store   UserStore
	groups  GroupStore
	tokens  TokenStore
	attrs   AttributeStore
	limiter *limiter

	// extended turns on the extensible-attribute pass-through. Off
	// by default: with it off the registry is never consulted and a user
	// serialises exactly as it did before the feature existed.
	extended bool

	// externalURL overrides the Host header when set, which matters behind a
	// TLS-terminating proxy: r.TLS is nil there, so derived links would be http.
	externalURL string
}

func NewHandler(s UserStore, groups GroupStore, tokens TokenStore, attrs AttributeStore) *Handler {
	return &Handler{
		store:       s,
		groups:      groups,
		tokens:      tokens,
		attrs:       attrs,
		limiter:     limiterFromEnv(),
		extended:    os.Getenv("SCIM_EXTENDED_ATTRIBUTES") == "1",
		externalURL: strings.TrimSuffix(os.Getenv("SCIM_BASE_URL"), "/"),
	}
}

// Routes applies token auth itself rather than leaving it to the caller. A
// tenant with no matching, live token rejects every request under its path
// rather than serving openly.
//
// Every path carries {tenantID}: one SCIMage deployment serves many customer
// organizations, each provisioning through its own URL and its own issued
// token, isolated from every other tenant's data by that segment.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /scim/v2/{tenantID}/Users", h.create)
	mux.HandleFunc("GET /scim/v2/{tenantID}/Users", h.list)
	mux.HandleFunc("GET /scim/v2/{tenantID}/Users/{id}", h.get)
	mux.HandleFunc("PUT /scim/v2/{tenantID}/Users/{id}", h.replace)
	mux.HandleFunc("PATCH /scim/v2/{tenantID}/Users/{id}", h.patch)
	mux.HandleFunc("DELETE /scim/v2/{tenantID}/Users/{id}", h.deactivate)

	mux.HandleFunc("POST /scim/v2/{tenantID}/Groups", h.createGroup)
	mux.HandleFunc("GET /scim/v2/{tenantID}/Groups", h.listGroups)
	mux.HandleFunc("GET /scim/v2/{tenantID}/Groups/{id}", h.getGroup)
	mux.HandleFunc("PUT /scim/v2/{tenantID}/Groups/{id}", h.replaceGroup)
	mux.HandleFunc("PATCH /scim/v2/{tenantID}/Groups/{id}", h.patchGroup)
	mux.HandleFunc("DELETE /scim/v2/{tenantID}/Groups/{id}", h.deleteGroup)

	// Discovery (RFC 7644 §4). A client reads these before provisioning, to
	// learn what this server supports.
	mux.HandleFunc("GET /scim/v2/{tenantID}/ServiceProviderConfig", h.serviceProviderConfig)
	mux.HandleFunc("GET /scim/v2/{tenantID}/ResourceTypes", h.resourceTypes)
	mux.HandleFunc("GET /scim/v2/{tenantID}/Schemas", h.schemas)

	// Without these, an unrouted method or path gets net/http's plain-text
	// error, which a SCIM client can't parse.
	mux.HandleFunc("/scim/v2/{tenantID}/Users", methodNotAllowed)
	mux.HandleFunc("/scim/v2/{tenantID}/Users/{id}", methodNotAllowed)
	mux.HandleFunc("/scim/v2/{tenantID}/Groups", methodNotAllowed)
	mux.HandleFunc("/scim/v2/{tenantID}/Groups/{id}", methodNotAllowed)
	mux.HandleFunc("/", unknownResource)

	// Throttle inside auth, so each authenticated caller has its own budget.
	// Request logging wraps the outside, to record what a client actually sent
	// including the calls auth rejects.
	return logRequests(requireToken(h.tokens)(h.limiter.throttle(mux)))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	in, body, ok := decodeUser(w, r)
	if !ok {
		return
	}

	registered, err := h.registeredAttributes(r)
	if err != nil {
		serverError(w, "create user", err)
		return
	}

	su := toStoreUser(in)
	su.ExtendedAttributes = captureExtended(body, registered)
	created, err := h.store.CreateUser(r.Context(), r.PathValue("tenantID"), &su, h.auditRecord(r))
	if err != nil {
		if errors.Is(err, store.ErrDuplicateUserName) {
			writeError(w, http.StatusConflict, "uniqueness", "userName is already in use")
			return
		}
		serverError(w, "create user", err)
		return
	}

	out := fromStoreUser(created, h.baseURL(r))
	w.Header().Set("Location", out.Meta.Location)
	writeJSON(w, http.StatusCreated, shapeUser(out, created.ExtendedAttributes))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	u, err := h.store.GetUser(r.Context(), r.PathValue("tenantID"), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFound(w)
			return
		}
		serverError(w, "get user", err)
		return
	}

	writeJSON(w, http.StatusOK, shapeUser(fromStoreUser(u, h.baseURL(r)), u.ExtendedAttributes))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	// Identity providers resolve a user by filter before deciding whether to
	// create one, so an unsupported expression is refused rather than answered
	// with an unfiltered list, which would read as a match.
	filter, err := parseFilter(r.URL.Query().Get("filter"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalidFilter", err.Error())
		return
	}

	startIndex, count := pageParams(r)

	users, total, err := h.store.ListUsers(r.Context(), r.PathValue("tenantID"), count, startIndex-1, filter)
	if err != nil {
		serverError(w, "list users", err)
		return
	}

	base := h.baseURL(r)
	resources := make([]json.RawMessage, 0, len(users))
	for i := range users {
		resources = append(resources, shapeUser(fromStoreUser(&users[i], base), users[i].ExtendedAttributes))
	}

	writeJSON(w, http.StatusOK, listOf[json.RawMessage]{
		Schemas:      []string{listSchema},
		TotalResults: total,
		ItemsPerPage: len(resources),
		StartIndex:   startIndex,
		Resources:    resources,
	})
}

func (h *Handler) replace(w http.ResponseWriter, r *http.Request) {
	in, body, ok := decodeUser(w, r)
	if !ok {
		return
	}

	tenantID, id := r.PathValue("tenantID"), r.PathValue("id")

	// id is readOnly in RFC 7643 §3.1. A body naming a different user is a
	// client bug worth refusing rather than silently ignoring.
	if in.ID != "" && in.ID != id {
		writeError(w, http.StatusBadRequest, "mutability", "id in the body does not match the request path")
		return
	}

	registered, err := h.registeredAttributes(r)
	if err != nil {
		serverError(w, "update user", err)
		return
	}

	su := toStoreUser(in)
	su.ExtendedAttributes = captureExtended(body, registered)
	changed, err := h.store.UpdateUser(r.Context(), tenantID, id, &su, h.auditRecord(r))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			notFound(w)
		case errors.Is(err, store.ErrDuplicateUserName):
			writeError(w, http.StatusConflict, "uniqueness", "userName is already in use")
		default:
			serverError(w, "update user", err)
		}
		return
	}

	writeJSON(w, http.StatusOK, shapeUser(fromStoreUser(changed.After, h.baseURL(r)), changed.After.ExtendedAttributes))
}

// PATCH applies a set of operations to the stored resource. It reads the
// current row, folds the operations onto it, and writes the result back through
// the same full-replace path a PUT uses — so a partial update still gets the
// audit entry and the uniqueness checks.
//
// The read and the write are separate statements, so a concurrent change to the
// same user between them would be overwritten. Provisioning traffic for one user
// is serial in practice; making this airtight needs the operations applied
// inside the store's transaction.
func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	tenantID, id := r.PathValue("tenantID"), r.PathValue("id")

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var ops PatchOp
	if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
		writeError(w, http.StatusBadRequest, "invalidSyntax", "request body is not valid JSON")
		return
	}

	existing, err := h.store.GetUser(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFound(w)
			return
		}
		serverError(w, "get user for patch", err)
		return
	}

	registered, err := h.registeredAttributes(r)
	if err != nil {
		serverError(w, "patch user", err)
		return
	}

	// Operations that target a registered extended attribute are peeled off and
	// applied to the JSONB blob; everything else runs through the core PATCH
	// path unchanged, so an unregistered non-core path still yields invalidPath.
	coreOps, extOps, err := splitExtendedOps(ops, registered)
	if err != nil {
		serverError(w, "patch user", err)
		return
	}

	current := fromStoreUser(existing, h.baseURL(r))
	patched := current
	// Run the core PATCH when there are core ops, or when there are no
	// extended ops either — the latter lets applyPatch report an empty
	// Operations list, so an extended-only patch is the only case that skips it.
	if len(coreOps.Operations) > 0 || len(extOps) == 0 {
		patched, err = applyPatch(current, coreOps)
		if err != nil {
			scimType := "invalidValue"
			if errors.Is(err, errUnsupportedPath) {
				scimType = "invalidPath"
			}
			writeError(w, http.StatusBadRequest, scimType, err.Error())
			return
		}
	}

	if detail := validate(patched); detail != "" {
		writeError(w, http.StatusBadRequest, "invalidValue", detail)
		return
	}

	newExtended, err := applyExtendedOps(existing.ExtendedAttributes, extOps)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalidValue", "an extended attribute value is not valid JSON")
		return
	}

	su := toStoreUser(patched)
	su.ExtendedAttributes = newExtended
	changed, err := h.store.UpdateUser(r.Context(), tenantID, id, &su, h.auditRecord(r))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			notFound(w)
		case errors.Is(err, store.ErrDuplicateUserName):
			writeError(w, http.StatusConflict, "uniqueness", "userName is already in use")
		default:
			serverError(w, "patch user", err)
		}
		return
	}

	writeJSON(w, http.StatusOK, shapeUser(fromStoreUser(changed.After, h.baseURL(r)), changed.After.ExtendedAttributes))
}

// DELETE is a soft delete: the row survives with active=false.
func (h *Handler) deactivate(w http.ResponseWriter, r *http.Request) {
	tenantID, id := r.PathValue("tenantID"), r.PathValue("id")

	if _, err := h.store.DeactivateUser(r.Context(), tenantID, id, h.auditRecord(r)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFound(w)
			return
		}
		serverError(w, "deactivate user", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeGroup(w, r)
	if !ok {
		return
	}

	sg := toStoreGroup(in)
	created, err := h.groups.CreateGroup(r.Context(), r.PathValue("tenantID"), &sg, h.auditRecord(r))
	if err != nil {
		writeGroupError(w, "create group", err)
		return
	}

	out := fromStoreGroup(created, h.baseURL(r))
	w.Header().Set("Location", out.Meta.Location)
	writeJSON(w, http.StatusCreated, shapeGroup(out, !membersExcluded(r)))
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	g, err := h.groups.GetGroup(r.Context(), r.PathValue("tenantID"), r.PathValue("id"))
	if err != nil {
		writeGroupError(w, "get group", err)
		return
	}

	writeJSON(w, http.StatusOK, shapeGroup(fromStoreGroup(g, h.baseURL(r)), !membersExcluded(r)))
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	filter, err := parseGroupFilter(r.URL.Query().Get("filter"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalidFilter", err.Error())
		return
	}

	startIndex, count := pageParams(r)

	groups, total, err := h.groups.ListGroups(r.Context(), r.PathValue("tenantID"), count, startIndex-1, filter)
	if err != nil {
		serverError(w, "list groups", err)
		return
	}

	base := h.baseURL(r)
	includeMembers := !membersExcluded(r)
	resources := make([]json.RawMessage, 0, len(groups))
	for i := range groups {
		resources = append(resources, shapeGroup(fromStoreGroup(&groups[i], base), includeMembers))
	}

	writeJSON(w, http.StatusOK, listOf[json.RawMessage]{
		Schemas:      []string{listSchema},
		TotalResults: total,
		ItemsPerPage: len(resources),
		StartIndex:   startIndex,
		Resources:    resources,
	})
}

func (h *Handler) replaceGroup(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeGroup(w, r)
	if !ok {
		return
	}

	tenantID, id := r.PathValue("tenantID"), r.PathValue("id")

	if in.ID != "" && in.ID != id {
		writeError(w, http.StatusBadRequest, "mutability", "id in the body does not match the request path")
		return
	}

	sg := toStoreGroup(in)
	changed, err := h.groups.UpdateGroup(r.Context(), tenantID, id, &sg, h.auditRecord(r))
	if err != nil {
		writeGroupError(w, "update group", err)
		return
	}

	writeJSON(w, http.StatusOK, shapeGroup(fromStoreGroup(changed.After, h.baseURL(r)), !membersExcluded(r)))
}

// patchGroup mirrors patch: read the current resource, fold the operations
// onto it, and write the result back through the same full-replace path a
// PUT uses — membership included, so a member add/remove gets the same
// audit entry and uniqueness checks a whole-resource replace does.
func (h *Handler) patchGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, id := r.PathValue("tenantID"), r.PathValue("id")

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var ops PatchOp
	if err := json.NewDecoder(r.Body).Decode(&ops); err != nil {
		writeError(w, http.StatusBadRequest, "invalidSyntax", "request body is not valid JSON")
		return
	}

	existing, err := h.groups.GetGroup(r.Context(), tenantID, id)
	if err != nil {
		writeGroupError(w, "get group for patch", err)
		return
	}

	patched, err := applyGroupPatch(fromStoreGroup(existing, h.baseURL(r)), ops)
	if err != nil {
		scimType := "invalidValue"
		if errors.Is(err, errUnsupportedPath) {
			scimType = "invalidPath"
		}
		writeError(w, http.StatusBadRequest, scimType, err.Error())
		return
	}

	if detail := validateGroup(patched); detail != "" {
		writeError(w, http.StatusBadRequest, "invalidValue", detail)
		return
	}

	sg := toStoreGroup(patched)
	changed, err := h.groups.UpdateGroup(r.Context(), tenantID, id, &sg, h.auditRecord(r))
	if err != nil {
		writeGroupError(w, "patch group", err)
		return
	}

	writeJSON(w, http.StatusOK, shapeGroup(fromStoreGroup(changed.After, h.baseURL(r)), !membersExcluded(r)))
}

// deleteGroup is a hard delete: unlike Users, the Group schema has no active
// attribute to soft-delete into.
func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	tenantID, id := r.PathValue("tenantID"), r.PathValue("id")

	if _, err := h.groups.DeleteGroup(r.Context(), tenantID, id, h.auditRecord(r)); err != nil {
		writeGroupError(w, "delete group", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// membersExcluded reports whether the request asked to leave members out via
// excludedAttributes=members — the one exclusion Okta and Entra actually send
// on group reads (to avoid pulling large member lists). The full
// attributes/excludedAttributes projection grammar isn't implemented; only
// this member-list suppression is.
func membersExcluded(r *http.Request) bool {
	for _, field := range strings.Split(r.URL.Query().Get("excludedAttributes"), ",") {
		if strings.EqualFold(strings.TrimSpace(field), "members") {
			return true
		}
	}
	return false
}

// shapeGroup renders a group response, resolving the three states of the
// members attribute that a struct tag alone can't express: omitted when the
// caller asked to exclude it, an empty array when a group simply has none
// (rather than dropping the key, which some IdP clients choke on), or the
// populated list. It round-trips through a map so everything else stays
// exactly as the typed value marshals. The marshal steps can't fail for a
// Group (a fixed shape of strings and slices); the guards only satisfy
// errcheck, logging and returning whatever bytes exist rather than failing
// the request.
func shapeGroup(g Group, includeMembers bool) json.RawMessage {
	b, err := json.Marshal(g)
	if err != nil {
		slog.Error("marshal group", "error", err)
		return b
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		slog.Error("reshape group", "error", err)
		return b
	}

	if includeMembers {
		if _, ok := m["members"]; !ok {
			m["members"] = json.RawMessage("[]")
		}
	} else {
		delete(m, "members")
	}

	out, err := json.Marshal(m)
	if err != nil {
		slog.Error("marshal shaped group", "error", err)
		return b
	}
	return out
}

// auditRecord is the caller's identity, resolved by requireToken and read
// back out of the request context. The store writes the entry inside the
// mutation's transaction, so there is no path that changes a user without
// recording it — the same reasoning as Routes applying auth itself.
func (h *Handler) auditRecord(r *http.Request) store.AuditRecord {
	id := identityFromContext(r.Context())
	return store.AuditRecord{ActorToken: id.KeyID, ActorIP: clientIP(r), TenantID: id.TenantID}
}

// X-Forwarded-For is ignored: it is caller-controlled, and trusting it would
// let anyone forge the IP in the audit trail.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// decodeUser writes the error response itself and reports whether the payload
// is usable. It returns both the typed user and the raw top-level keys, so the
// extensible-attribute capture can pull registered names the typed struct
// doesn't model. Unknown fields are otherwise accepted and dropped: real IdPs
// send externalId, groups and enterprise-extension attributes this server
// doesn't model.
func decodeUser(w http.ResponseWriter, r *http.Request) (User, map[string]json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "", "request body is too large")
			return User{}, nil, false
		}
		writeError(w, http.StatusBadRequest, "invalidSyntax", "request body is not valid JSON")
		return User{}, nil, false
	}

	var u User
	if err := json.Unmarshal(raw, &u); err != nil {
		writeError(w, http.StatusBadRequest, "invalidSyntax", "request body is not valid JSON")
		return User{}, nil, false
	}

	if detail := validate(u); detail != "" {
		writeError(w, http.StatusBadRequest, "invalidValue", detail)
		return User{}, nil, false
	}

	// The typed decode above already proved the body is a JSON object, so this
	// second pass into a key map can't fail on shape.
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)

	return u, body, true
}

// validate returns an empty string when the payload is acceptable. Extension
// URNs in schemas are fine; only the core User schema has to be present.
func validate(u User) string {
	if !slices.Contains(u.Schemas, userSchema) {
		return "schemas must contain " + userSchema
	}

	userName := strings.TrimSpace(u.UserName)
	if userName == "" {
		return "userName is required"
	}

	var given, family string
	if u.Name != nil {
		given, family = u.Name.GivenName, u.Name.FamilyName
	}

	for _, f := range [][2]string{
		{"userName", userName},
		{"name.givenName", given},
		{"name.familyName", family},
		{"emails.value", primaryEmail(u.Emails)},
	} {
		if len(f[1]) > maxAttrLen {
			return f[0] + " exceeds " + strconv.Itoa(maxAttrLen) + " characters"
		}
	}

	return ""
}

// decodeGroup mirrors decodeUser.
func decodeGroup(w http.ResponseWriter, r *http.Request) (Group, bool) {
	var g Group

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "", "request body is too large")
			return g, false
		}
		writeError(w, http.StatusBadRequest, "invalidSyntax", "request body is not valid JSON")
		return g, false
	}

	if detail := validateGroup(g); detail != "" {
		writeError(w, http.StatusBadRequest, "invalidValue", detail)
		return g, false
	}

	return g, true
}

func validateGroup(g Group) string {
	if !slices.Contains(g.Schemas, groupSchema) {
		return "schemas must contain " + groupSchema
	}

	displayName := strings.TrimSpace(g.DisplayName)
	if displayName == "" {
		return "displayName is required"
	}
	if len(displayName) > maxAttrLen {
		return "displayName exceeds " + strconv.Itoa(maxAttrLen) + " characters"
	}

	return ""
}

// writeGroupError maps the Group store's sentinels onto SCIM responses, the
// same job the switch statements inline in each User handler do — pulled
// into one function here since every Group handler needs the same three
// cases (ErrGroupNotFound was ErrNotFound's own error text for Users, but
// Groups get their own sentinel so a wrapped error reads correctly in logs).
func writeGroupError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, store.ErrGroupNotFound):
		notFoundGroup(w)
	case errors.Is(err, store.ErrDuplicateGroupName):
		writeError(w, http.StatusConflict, "uniqueness", "displayName is already in use")
	case errors.Is(err, store.ErrInvalidMember):
		writeError(w, http.StatusBadRequest, "invalidValue", "members references an unknown or foreign user")
	default:
		serverError(w, op, err)
	}
}

func notFoundGroup(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "", "group not found")
}

// startIndex is 1-based and count is a page size (RFC 7644 §3.4.2.4); both are
// floored rather than rejected, per the spec. The store caps count at
// MaxPageSize.
func pageParams(r *http.Request) (startIndex, count int) {
	q := r.URL.Query()

	startIndex = 1
	if n, err := strconv.Atoi(q.Get("startIndex")); err == nil && n > 1 {
		startIndex = n
	}

	count = store.MaxPageSize
	if n, err := strconv.Atoi(q.Get("count")); err == nil {
		count = max(n, 0)
	}

	return startIndex, count
}

// baseURL is this tenant's own root, not the server's: every Location and
// meta.location a client sees has to point back through the same
// {tenantID} segment the client itself called through.
func (h *Handler) baseURL(r *http.Request) string {
	root := h.externalURL
	if root == "" {
		if r.TLS != nil {
			root = "https://" + r.Host
		} else {
			root = "http://" + r.Host
		}
	}
	return root + "/scim/v2/" + r.PathValue("tenantID")
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusMethodNotAllowed, "", r.Method+" is not supported on this resource")
}

func unknownResource(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "", "unknown resource")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, scimType, detail string) {
	writeJSON(w, status, Error{
		Schemas:  []string{errorSchema},
		Status:   strconv.Itoa(status),
		ScimType: scimType,
		Detail:   detail,
	})
}

func notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "", "user not found")
}

// serverError keeps the cause in the log and out of the response.
func serverError(w http.ResponseWriter, op string, err error) {
	slog.Error(op, "error", err)
	writeError(w, http.StatusInternalServerError, "", "internal server error")
}
