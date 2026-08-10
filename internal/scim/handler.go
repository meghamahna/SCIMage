package scim

import (
	"encoding/json"
	"errors"
	"log"
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
	store *store.Store

	// externalURL overrides the Host header when set, which matters behind a
	// TLS-terminating proxy: r.TLS is nil there, so derived links would be http.
	externalURL string
}

func NewHandler(s *store.Store) *Handler {
	return &Handler{
		store:       s,
		externalURL: strings.TrimSuffix(os.Getenv("SCIM_BASE_URL"), "/"),
	}
}

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

	return mux
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

	su := toStoreUser(in)
	updated, err := h.store.UpdateUser(r.Context(), r.PathValue("id"), &su)
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

	writeJSON(w, http.StatusOK, fromStoreUser(updated, h.baseURL(r)))
}

// DELETE is a soft delete: the row survives with active=false.
func (h *Handler) deactivate(w http.ResponseWriter, r *http.Request) {
	if _, err := h.store.DeactivateUser(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFound(w)
			return
		}
		serverError(w, "deactivate user", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
