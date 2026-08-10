package scim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/meghamahna/SCIMage/internal/audit"
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
	store *store.Store
	token string
	audit *audit.Logger

	// actor identifies the caller in audit entries: a short fingerprint of the
	// token, never the token. 32 bits is plenty to tell callers apart and
	// useless for recovering a 256-bit secret.
	actor   string
	limiter *limiter

	// externalURL overrides the Host header when set, which matters behind a
	// TLS-terminating proxy: r.TLS is nil there, so derived links would be http.
	externalURL string
}

func NewHandler(s *store.Store, token string, auditLog *audit.Logger) *Handler {
	sum := sha256.Sum256([]byte(token))

	return &Handler{
		store:       s,
		token:       token,
		audit:       auditLog,
		actor:       "tok_" + hex.EncodeToString(sum[:4]),
		limiter:     limiterFromEnv(),
		externalURL: strings.TrimSuffix(os.Getenv("SCIM_BASE_URL"), "/"),
	}
}

// Routes applies bearer auth itself rather than leaving it to the caller. A
// missing or too-short token rejects every request rather than serving openly.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /Users", h.create)
	mux.HandleFunc("GET /Users", h.list)
	mux.HandleFunc("GET /Users/{id}", h.get)
	mux.HandleFunc("PUT /Users/{id}", h.replace)
	mux.HandleFunc("DELETE /Users/{id}", h.deactivate)

	// Without these, an unrouted method or path gets net/http's plain-text
	// error, which a SCIM client can't parse.
	mux.HandleFunc("/Users", methodNotAllowed)
	mux.HandleFunc("/Users/{id}", methodNotAllowed)
	mux.HandleFunc("/", unknownResource)

	// Throttle inside auth, so an unauthenticated flood can't spend a real
	// caller's budget.
	return requireBearer(h.token)(h.limiter.throttle(h.actor, mux))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeUser(w, r)
	if !ok {
		return
	}

	su := toStoreUser(in)
	created, err := h.store.CreateUser(r.Context(), &su)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateUserName) {
			h.record(r, audit.ActionCreate, "", audit.ResultDenied, "duplicate userName", nil, nil)
			writeError(w, http.StatusConflict, "uniqueness", "userName is already in use")
			return
		}
		h.record(r, audit.ActionCreate, "", audit.ResultError, err.Error(), nil, nil)
		serverError(w, "create user", err)
		return
	}
	h.record(r, audit.ActionCreate, created.ID, audit.ResultSuccess, "", nil, created)

	out := fromStoreUser(created, h.baseURL(r))
	w.Header().Set("Location", out.Meta.Location)
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	u, err := h.store.GetUser(r.Context(), r.PathValue("id"))
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
	// Okta and Entra probe with a userName filter before every create. Answering
	// an unfiltered list would read as "matched" and point them at the wrong user.
	if r.URL.Query().Has("filter") {
		writeError(w, http.StatusBadRequest, "invalidFilter", "filtering is not supported")
		return
	}

	startIndex, count := pageParams(r)

	users, total, err := h.store.ListUsers(r.Context(), count, startIndex-1)
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

	id := r.PathValue("id")

	// id is readOnly in RFC 7643 §3.1. A body naming a different user is a
	// client bug worth refusing rather than silently ignoring.
	if in.ID != "" && in.ID != id {
		writeError(w, http.StatusBadRequest, "mutability", "id in the body does not match the request path")
		return
	}

	su := toStoreUser(in)
	changed, err := h.store.UpdateUser(r.Context(), id, &su)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			h.record(r, audit.ActionReplace, id, audit.ResultDenied, "no such user", nil, nil)
			notFound(w)
		case errors.Is(err, store.ErrDuplicateUserName):
			h.record(r, audit.ActionReplace, id, audit.ResultDenied, "duplicate userName", nil, nil)
			writeError(w, http.StatusConflict, "uniqueness", "userName is already in use")
		default:
			h.record(r, audit.ActionReplace, id, audit.ResultError, err.Error(), nil, nil)
			serverError(w, "update user", err)
		}
		return
	}
	h.record(r, audit.ActionReplace, id, audit.ResultSuccess, "", changed.Before, changed.After)

	writeJSON(w, http.StatusOK, fromStoreUser(changed.After, h.baseURL(r)))
}

// DELETE is a soft delete: the row survives with active=false.
func (h *Handler) deactivate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	changed, err := h.store.DeactivateUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.record(r, audit.ActionDeactivate, id, audit.ResultDenied, "no such user", nil, nil)
			notFound(w)
			return
		}
		h.record(r, audit.ActionDeactivate, id, audit.ResultError, err.Error(), nil, nil)
		serverError(w, "deactivate user", err)
		return
	}
	h.record(r, audit.ActionDeactivate, id, audit.ResultSuccess, "", changed.Before, changed.After)

	w.WriteHeader(http.StatusNoContent)
}

// record writes one audit line. Deliberately called at every exit of a mutating
// handler, including refusals — a burst of denied deactivations is exactly the
// pattern a reviewer wants to see, and it is invisible if only successes land.
func (h *Handler) record(r *http.Request, action, targetID, result, detail string, before, after *store.User) {
	if h.audit == nil {
		return
	}

	h.audit.Write(audit.Entry{
		Action:   action,
		Actor:    audit.Actor{Token: h.actor, IP: clientIP(r)},
		TargetID: targetID,
		Result:   result,
		Detail:   detail,
		Before:   auditUser(before),
		After:    auditUser(after),
	})
}

func auditUser(u *store.User) *audit.User {
	if u == nil {
		return nil
	}

	a := audit.User{
		ID:        u.ID,
		UserName:  u.UserName,
		Active:    u.Active,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
	if u.GivenName != nil {
		a.GivenName = *u.GivenName
	}
	if u.FamilyName != nil {
		a.FamilyName = *u.FamilyName
	}
	if u.Email != nil {
		a.Email = *u.Email
	}
	return &a
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

func (h *Handler) baseURL(r *http.Request) string {
	if h.externalURL != "" {
		return h.externalURL
	}
	if r.TLS != nil {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

// PATCH is a real SCIM operation this server doesn't implement; anything else
// on a known path is a genuine method error.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		writeError(w, http.StatusNotImplemented, "", "PATCH is not supported; use PUT")
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "", r.Method+" is not supported on this resource")
}

func unknownResource(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "", "unknown resource")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("scim: write response: %v", err)
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
	log.Printf("scim: %s: %v", op, err)
	writeError(w, http.StatusInternalServerError, "", "internal server error")
}
