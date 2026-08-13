package scim

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// errUnsupportedPath separates "this attribute isn't one we store" from "the
// value was wrong", so the handler can answer with the right scimType.
var errUnsupportedPath = errors.New("unsupported path")

const patchOpSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"

// PatchOp is a PATCH request body (RFC 7644 §3.5.2).
type PatchOp struct {
	Schemas    []string    `json:"schemas"`
	Operations []Operation `json:"Operations"`
}

// Value is raw because its shape depends on the path: a string for
// name.givenName, a boolean for active, and an object for a pathless replace.
type Operation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// emailPath matches emails.value and the filtered forms clients send, such as
// emails[type eq "work"].value or emails[primary eq true].value. The schema
// stores one address, so every form targets the same field.
var emailPath = regexp.MustCompile(`^emails(\[[^\]]*\])?(\.value)?$`)

// applyPatch folds a PATCH body onto the resource as it currently stands and
// returns the result, which the caller persists as a whole. SCIM PATCH is
// defined as a set of operations, but this server stores a fixed set of
// attributes, so each operation resolves to a field assignment.
func applyPatch(current User, patch PatchOp) (User, error) {
	if len(patch.Operations) == 0 {
		return current, fmt.Errorf("Operations is required")
	}

	for _, op := range patch.Operations {
		var err error
		// Entra capitalises the op ("Replace"), which RFC 7644 §3.5.2 does not
		// require to be lowercase either.
		switch strings.ToLower(strings.TrimSpace(op.Op)) {
		case "add", "replace":
			current, err = applyValue(current, op)
		case "remove":
			current, err = clearPath(current, op.Path)
		default:
			err = fmt.Errorf("unsupported op %q", op.Op)
		}
		if err != nil {
			return current, err
		}
	}

	return current, nil
}

// applyValue handles add and replace, which differ only for multi-valued
// attributes — and this schema holds one email, so they coincide.
func applyValue(u User, op Operation) (User, error) {
	// A pathless operation carries an object of attributes to apply.
	if strings.TrimSpace(op.Path) == "" {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(op.Value, &attrs); err != nil {
			return u, fmt.Errorf("a value without a path must be an object of attributes")
		}

		for path, raw := range attrs {
			var err error
			if u, err = setPath(u, path, raw); err != nil {
				return u, err
			}
		}
		return u, nil
	}

	return setPath(u, op.Path, op.Value)
}

func setPath(u User, path string, raw json.RawMessage) (User, error) {
	switch key := strings.ToLower(strings.TrimSpace(path)); {
	case key == "username":
		return u, assignString(&u.UserName, raw, path)

	case key == "externalid":
		return u, assignString(&u.ExternalID, raw, path)

	case key == "active":
		var b Bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return u, fmt.Errorf("active: %w", err)
		}
		u.Active = &b
		return u, nil

	case key == "name.givenname", key == "name.familyname":
		if u.Name == nil {
			u.Name = &Name{}
		}
		target := &u.Name.GivenName
		if key == "name.familyname" {
			target = &u.Name.FamilyName
		}
		return u, assignString(target, raw, path)

	case key == "name":
		var n Name
		if err := json.Unmarshal(raw, &n); err != nil {
			return u, fmt.Errorf("name: %w", err)
		}
		u.Name = &n
		return u, nil

	case emailPath.MatchString(key):
		return setEmail(u, raw, path)

	default:
		return u, fmt.Errorf("path %q: %w", path, errUnsupportedPath)
	}
}

// setEmail accepts the address as a bare string, as one object, or as an array
// of them — clients send all three depending on the path form used.
func setEmail(u User, raw json.RawMessage, path string) (User, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		u.Emails = []Email{{Value: value, Primary: true}}
		return u, nil
	}

	var one Email
	if err := json.Unmarshal(raw, &one); err == nil && one.Value != "" {
		one.Primary = true
		u.Emails = []Email{one}
		return u, nil
	}

	var many []Email
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 {
		u.Emails = []Email{{Value: primaryEmail(many), Primary: true}}
		return u, nil
	}

	return u, fmt.Errorf("%s: expected an email address", path)
}

func clearPath(u User, path string) (User, error) {
	switch key := strings.ToLower(strings.TrimSpace(path)); {
	case key == "":
		return u, fmt.Errorf("remove requires a path")
	case key == "externalid":
		u.ExternalID = ""
	case key == "name.givenname":
		if u.Name != nil {
			u.Name.GivenName = ""
		}
	case key == "name.familyname":
		if u.Name != nil {
			u.Name.FamilyName = ""
		}
	case key == "name":
		u.Name = nil
	case key == "active":
		// Removing active restores the default rather than leaving it unset.
		active := Bool(true)
		u.Active = &active
	case emailPath.MatchString(key):
		u.Emails = nil
	default:
		return u, fmt.Errorf("path %q: %w", path, errUnsupportedPath)
	}
	return u, nil
}

func assignString(target *string, raw json.RawMessage, path string) error {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("%s: expected a string", path)
	}
	*target = v
	return nil
}

// membersPath matches the group's own multi-valued attribute, bare or with
// the single-member filter an IdP sends to remove one id:
// members[value eq "<id>"].
var membersPath = regexp.MustCompile(`^members(\[[^\]]*\])?$`)

// (?i) rather than lowercasing the input: the captured group is a member id,
// and a case-sensitive external identifier there must survive extraction
// unchanged — only the members/value/eq keywords are case-insensitive.
var memberFilterValue = regexp.MustCompile(`(?i)^members\[\s*value\s+eq\s+"?([^"\]]*)"?\s*\]$`)

// applyGroupPatch mirrors applyPatch, but members is genuinely multi-valued —
// unlike User's single-valued attributes, add and replace differ (append vs.
// overwrite), so the op threads down as isAdd rather than being folded away.
func applyGroupPatch(current Group, patch PatchOp) (Group, error) {
	if len(patch.Operations) == 0 {
		return current, fmt.Errorf("Operations is required")
	}

	for _, op := range patch.Operations {
		var err error
		switch strings.ToLower(strings.TrimSpace(op.Op)) {
		case "add":
			current, err = applyGroupValue(current, op, true)
		case "replace":
			current, err = applyGroupValue(current, op, false)
		case "remove":
			current, err = clearGroupPath(current, op.Path)
		default:
			err = fmt.Errorf("unsupported op %q", op.Op)
		}
		if err != nil {
			return current, err
		}
	}

	return current, nil
}

func applyGroupValue(g Group, op Operation, isAdd bool) (Group, error) {
	if strings.TrimSpace(op.Path) == "" {
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal(op.Value, &attrs); err != nil {
			return g, fmt.Errorf("a value without a path must be an object of attributes")
		}

		for path, raw := range attrs {
			var err error
			if g, err = setGroupPath(g, path, raw, isAdd); err != nil {
				return g, err
			}
		}
		return g, nil
	}

	return setGroupPath(g, op.Path, op.Value, isAdd)
}

func setGroupPath(g Group, path string, raw json.RawMessage, isAdd bool) (Group, error) {
	switch key := strings.ToLower(strings.TrimSpace(path)); {
	case key == "displayname":
		return g, assignString(&g.DisplayName, raw, path)

	case key == "externalid":
		return g, assignString(&g.ExternalID, raw, path)

	case membersPath.MatchString(key):
		return setMembers(g, raw, isAdd)

	default:
		return g, fmt.Errorf("path %q: %w", path, errUnsupportedPath)
	}
}

// setMembers accepts a single member object, a bare id string, or an array of
// either — clients send all these shapes depending on the path form used.
// Adding dedupes against the existing set rather than erroring on a repeat,
// since a re-push of the same membership is a common, harmless IdP retry.
func setMembers(g Group, raw json.RawMessage, isAdd bool) (Group, error) {
	values, err := decodeMemberValues(raw)
	if err != nil {
		return g, err
	}

	if !isAdd {
		g.Members = toMembers(values)
		return g, nil
	}

	seen := make(map[string]bool, len(g.Members))
	for _, m := range g.Members {
		seen[m.Value] = true
	}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			g.Members = append(g.Members, Member{Value: v})
		}
	}
	return g, nil
}

func toMembers(values []string) []Member {
	if len(values) == 0 {
		return nil
	}
	members := make([]Member, len(values))
	for i, v := range values {
		members[i] = Member{Value: v}
	}
	return members
}

// decodeMemberValues accepts a bare id string, one member object, or an
// array of member objects — the shapes a PATCH value takes depending on
// whether the client sent a filtered single-member path or a whole array.
func decodeMemberValues(raw json.RawMessage) ([]string, error) {
	var one Member
	if err := json.Unmarshal(raw, &one); err == nil && one.Value != "" {
		return []string{one.Value}, nil
	}

	var many []Member
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 {
		values := make([]string, 0, len(many))
		for _, m := range many {
			if m.Value != "" {
				values = append(values, m.Value)
			}
		}
		return values, nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return []string{s}, nil
	}

	return nil, fmt.Errorf("members: expected a member value or an array of them")
}

// trimmed, not lowered, is what a member id is extracted from below — a
// case-sensitive external identifier has to survive unchanged even though
// the path keywords around it are matched case-insensitively.
func clearGroupPath(g Group, path string) (Group, error) {
	trimmed := strings.TrimSpace(path)
	key := strings.ToLower(trimmed)
	switch {
	case key == "":
		return g, fmt.Errorf("remove requires a path")
	case key == "externalid":
		g.ExternalID = ""
	case key == "members":
		g.Members = nil
	case membersPath.MatchString(key):
		value, ok := filteredMemberValue(trimmed)
		if !ok {
			return g, fmt.Errorf("path %q: %w", path, errUnsupportedPath)
		}
		g.Members = removeMember(g.Members, value)
	default:
		return g, fmt.Errorf("path %q: %w", path, errUnsupportedPath)
	}
	return g, nil
}

func filteredMemberValue(path string) (string, bool) {
	m := memberFilterValue.FindStringSubmatch(path)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func removeMember(members []Member, value string) []Member {
	var out []Member
	for _, m := range members {
		if m.Value != value {
			out = append(out, m)
		}
	}
	return out
}
