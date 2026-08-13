package scim

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// Extensible attributes (Phase 14): a tenant can register extra top-level SCIM
// attribute names — known ones this server doesn't model as typed columns, or
// fully custom fields — and have them captured into a JSONB blob and merged
// back on reads, rather than dropped. The feature is inert unless the
// SCIM_EXTENDED_ATTRIBUTES flag is set and the tenant has registered a name.

// registeredAttributes returns the tenant's registered attributes as a lookup
// from lowercased name to canonical name, or nil when the feature is off or
// nothing is registered. SCIM attribute names are case-insensitive, so
// matching is done lower-cased while values are stored under the canonical
// registered form.
func (h *Handler) registeredAttributes(r *http.Request) (map[string]string, error) {
	if !h.extended || h.attrs == nil {
		return nil, nil
	}

	attrs, err := h.attrs.ListAttributes(r.Context(), r.PathValue("tenantID"))
	if err != nil {
		return nil, fmt.Errorf("list registered attributes: %w", err)
	}
	if len(attrs) == 0 {
		return nil, nil
	}

	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[strings.ToLower(a.Name)] = a.Name
	}
	return m, nil
}

// captureExtended pulls the registered keys out of the raw request body into
// the JSONB blob, matching case-insensitively but storing under the canonical
// registered name. It returns nil when nothing is captured, so a user without
// extended attributes keeps a NULL column exactly as before the feature.
func captureExtended(body map[string]json.RawMessage, registered map[string]string) json.RawMessage {
	if len(registered) == 0 || len(body) == 0 {
		return nil
	}

	ext := make(map[string]json.RawMessage)
	for k, v := range body {
		if canon, ok := registered[strings.ToLower(k)]; ok {
			ext[canon] = v
		}
	}
	if len(ext) == 0 {
		return nil
	}

	b, err := json.Marshal(ext)
	if err != nil {
		slog.Error("marshal extended attributes", "error", err)
		return nil
	}
	return b
}

// shapeUser renders a user response, merging the stored extended attributes
// back in at the top level. A core attribute always wins over a same-named
// extended key, so an extended value can never shadow id/userName/meta. An
// extension-URN key present in the blob is added to the schemas array, as
// RFC 7644 requires. With no extended attributes — the default — it returns
// the plain typed encoding untouched, so the common path pays nothing.
func shapeUser(u User, extended json.RawMessage) json.RawMessage {
	b, err := json.Marshal(u)
	if err != nil {
		slog.Error("marshal user", "error", err)
		return b
	}
	if len(extended) == 0 {
		return b
	}

	var core map[string]json.RawMessage
	if err := json.Unmarshal(b, &core); err != nil {
		slog.Error("reshape user", "error", err)
		return b
	}
	var ext map[string]json.RawMessage
	if err := json.Unmarshal(extended, &ext); err != nil {
		slog.Error("decode extended attributes", "error", err)
		return b
	}

	for k, v := range ext {
		if _, isCore := core[k]; isCore {
			continue
		}
		core[k] = v
		if strings.HasPrefix(k, "urn:") {
			addSchemaURN(core, k)
		}
	}

	out, err := json.Marshal(core)
	if err != nil {
		slog.Error("marshal shaped user", "error", err)
		return b
	}
	return out
}

// addSchemaURN appends an extension URN to the response schemas array if it
// isn't already there — SCIM requires a present extension to be declared in
// schemas.
func addSchemaURN(core map[string]json.RawMessage, urn string) {
	var schemas []string
	if raw, ok := core["schemas"]; ok {
		_ = json.Unmarshal(raw, &schemas)
	}
	for _, s := range schemas {
		if s == urn {
			return
		}
	}
	schemas = append(schemas, urn)
	if b, err := json.Marshal(schemas); err == nil {
		core["schemas"] = b
	}
}

// extendedOp is a PATCH operation resolved to a registered extended attribute.
type extendedOp struct {
	op    string // add | replace | remove
	name  string // canonical registered attribute name
	value json.RawMessage
}

// splitExtendedOps separates PATCH operations that target a registered
// extended attribute from those that hit the core typed schema. The core ops
// are returned as a PatchOp to feed applyPatch unchanged — so an unregistered,
// non-core path still yields invalidPath — and the extended ops are applied to
// the JSONB blob separately. A pathless value object is split key-by-key, so a
// single operation can update both core and extended attributes at once.
func splitExtendedOps(patch PatchOp, registered map[string]string) (PatchOp, []extendedOp, error) {
	if len(registered) == 0 {
		return patch, nil, nil
	}

	core := PatchOp{Schemas: patch.Schemas}
	var ext []extendedOp

	for _, op := range patch.Operations {
		action := strings.ToLower(strings.TrimSpace(op.Op))

		if path := strings.TrimSpace(op.Path); path != "" {
			if canon, ok := registered[strings.ToLower(path)]; ok {
				ext = append(ext, extendedOp{op: action, name: canon, value: op.Value})
			} else {
				core.Operations = append(core.Operations, op)
			}
			continue
		}

		// Pathless: only add/replace carry a value object; anything else is
		// left for applyPatch to report.
		if action != "add" && action != "replace" {
			core.Operations = append(core.Operations, op)
			continue
		}

		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(op.Value, &attrs); err != nil {
			core.Operations = append(core.Operations, op)
			continue
		}

		coreAttrs := make(map[string]json.RawMessage)
		for k, v := range attrs {
			if canon, ok := registered[strings.ToLower(k)]; ok {
				ext = append(ext, extendedOp{op: action, name: canon, value: v})
			} else {
				coreAttrs[k] = v
			}
		}
		if len(coreAttrs) > 0 {
			raw, err := json.Marshal(coreAttrs)
			if err != nil {
				return core, ext, err
			}
			core.Operations = append(core.Operations, Operation{Op: op.Op, Value: raw})
		}
	}

	return core, ext, nil
}

// applyExtendedOps folds the extended ops onto the existing blob and returns
// the new one (nil when it ends empty). With no ops it returns the existing
// blob unchanged, so a core-only PATCH preserves a user's extended attributes
// rather than clearing them.
func applyExtendedOps(existing json.RawMessage, ops []extendedOp) (json.RawMessage, error) {
	if len(ops) == 0 {
		return existing, nil
	}

	blob := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &blob); err != nil {
			return nil, err
		}
	}

	for _, o := range ops {
		switch o.op {
		case "add", "replace":
			blob[o.name] = o.value
		case "remove":
			delete(blob, o.name)
		}
	}

	if len(blob) == 0 {
		return nil, nil
	}
	return json.Marshal(blob)
}
