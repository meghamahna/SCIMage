package scim

import (
	"encoding/json"
	"errors"
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
	tokens  TokenStore
	limiter *limiter

	// externalURL overrides the Host header when set, which matters behind a
	// TLS-terminating proxy: r.TLS is nil there, so derived links would be http.
	externalURL string
}

func NewHandler(s UserStore, tokens TokenStore) *Handler {
	return &Handler{
		store:       s,
		tokens:      tokens,
		limiter:     limiterFromEnv(),
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

	// Discovery (RFC 7644 §4). A client reads these before provisioning, to
	// learn what this server supports.
	mux.HandleFunc("GET /scim/v2/{tenantID}/ServiceProviderConfig", h.serviceProviderConfig)
	mux.HandleFunc("GET /scim/v2/{tenantID}/ResourceTypes", h.resourceTypes)
	mux.HandleFunc("GET /scim/v2/{tenantID}/Schemas", h.schemas)

	// Without these, an unrouted method or path gets net/http's plain-text
	// error, which a SCIM client can't parse.
	mux.HandleFunc("/scim/v2/{tenantID}/Users", methodNotAllowed)
	mux.HandleFunc("/scim/v2/{tenantID}/Users/{id}", methodNotAllowed)
	mux.HandleFunc("/", unknownResource)

	// Throttle inside auth, so each authenticated caller has its own budget.
	// Request logging wraps the outside, to record what a client actually sent
	// including the calls auth rejects.
	return logRequests(requireToken(h.tokens)(h.limiter.throttle(mux)))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeUser(w, r)
	if !ok {
		return
	}

	su := toStoreUser(in)
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
	writeJSON(w, http.StatusCreated, out)
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

	writeJSON(w, http.StatusOK, fromStoreUser(u, h.baseURL(r)))
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
	resources := make([]User, 0, len(users))
	for i := range users {
		resources = append(resources, fromStoreUser(&users[i], base))
	}

	writeJSON(w, http.StatusOK, ListResponse{
		Schemas:      []string{listSchema},
		TotalResults: total,
		ItemsPerPage: len(resources),
		StartIndex:   startIndex,
		Resources:    resources,
	})
}

func (h *Handler) replace(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeUser(w, r)
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

	su := toStoreUser(in)
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

	writeJSON(w, http.StatusOK, fromStoreUser(changed.After, h.baseURL(r)))
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

	patched, err := applyPatch(fromStoreUser(existing, h.baseURL(r)), ops)
	if err != nil {
		scimType := "invalidValue"
		if errors.Is(err, errUnsupportedPath) {
			scimType = "invalidPath"
		}
		writeError(w, http.StatusBadRequest, scimType, err.Error())
		return
	}

	if detail := validate(patched); detail != "" {
		writeError(w, http.StatusBadRequest, "invalidValue", detail)
		return
	}

	su := toStoreUser(patched)
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

	writeJSON(w, http.StatusOK, fromStoreUser(changed.After, h.baseURL(r)))
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
// is usable. Unknown fields are accepted: real IdPs send externalId, groups and
// enterprise-extension attributes this server doesn't model.
func decodeUser(w http.ResponseWriter, r *http.Request) (User, bool) {
	var u User

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "", "request body is too large")
			return u, false
		}
		writeError(w, http.StatusBadRequest, "invalidSyntax", "request body is not valid JSON")
		return u, false
	}

	if detail := validate(u); detail != "" {
		writeError(w, http.StatusBadRequest, "invalidValue", detail)
		return u, false
	}

	return u, true
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
