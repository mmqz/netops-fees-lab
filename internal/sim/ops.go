package sim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"

	"github.com/mmqz/netops-lab/internal/domain"
)

// OpError reports the operation that failed during ApplyOps. Index is the
// 0-based index of the op within the submitted slice, Code the op code and
// Message the failure reason. It wraps domain.ErrOpFailed, so
// errors.Is(err, domain.ErrOpFailed) and errors.As(err, &*OpError) both work
// through the usual fmt.Errorf %w chains.
type OpError struct {
	// Index is the 0-based position of the failed op in the ops slice.
	Index int
	// Code is the op code of the failed op.
	Code string
	// Message is the failure reason (English, deterministic).
	Message string
}

// Error implements error.
func (e *OpError) Error() string {
	return fmt.Sprintf("op %d (%s): %s", e.Index, e.Code, e.Message)
}

// Unwrap makes errors.Is(err, domain.ErrOpFailed) work.
func (e *OpError) Unwrap() error { return domain.ErrOpFailed }

// Op is the forward-op payload of every external boundary (HTTP, MCP, CLI).
// Fields are validated per-op by ValidateOp; unused fields must be empty
// (strict: a set-but-unused field ⇒ domain.ErrInvalidOp).
//
// Note: the LinkID field (set_link_status / delete_link) is an addition to
// the originally dispatched struct — those two ops cannot be expressed
// without it (PROJECT_BRIEF_P2 §4 lists linkId for both). Flagged in the
// worklog.
type Op struct {
	Op           string  `json:"op"` // domain.OpXxx
	DeviceID     string  `json:"deviceId,omitempty"`
	Code         string  `json:"code,omitempty"`   // create_device
	Name         string  `json:"name,omitempty"`   // create_device
	Status       string  `json:"status,omitempty"` // set_*_status / create_* (optional, default up)
	InterfaceID  string  `json:"interfaceId,omitempty"`
	IPv4Addr     *string `json:"ipv4Addr,omitempty"` // config_interface_ipv4: nil = unconfigure
	IPv4Prefix   *int    `json:"ipv4Prefix,omitempty"`
	LinkID       string  `json:"linkId,omitempty"`       // set_link_status / delete_link
	AInterfaceID string  `json:"aInterfaceId,omitempty"` // create_link
	ZInterfaceID string  `json:"zInterfaceId,omitempty"`
	Label        string  `json:"label,omitempty"` // create_link optional
}

// interfaceNameRe is the allowed interface-name shape of create_interface
// (PROJECT_BRIEF_P2 §4).
var interfaceNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// opAllowedFields is the strict field-usage table of the ten forward ops
// (PROJECT_BRIEF_P2 §4): every field set on an op must be in its allowed
// set. create_device deliberately accepts no status — the §4 table lists
// only code and name for it.
var opAllowedFields = map[string][]string{
	domain.OpCreateDevice:        {"code", "name"},
	domain.OpSetDeviceStatus:     {"deviceId", "status"},
	domain.OpDeleteDevice:        {"deviceId"},
	domain.OpCreateInterface:     {"deviceId", "name", "status", "ipv4Addr", "ipv4Prefix"},
	domain.OpSetInterfaceStatus:  {"interfaceId", "status"},
	domain.OpConfigInterfaceIPv4: {"interfaceId", "ipv4Addr", "ipv4Prefix"},
	domain.OpDeleteInterface:     {"interfaceId"},
	domain.OpCreateLink:          {"aInterfaceId", "zInterfaceId", "status", "label"},
	domain.OpSetLinkStatus:       {"linkId", "status"},
	domain.OpDeleteLink:          {"linkId"},
}

// DecodeOps parses a raw JSON ops request body ({"ops":[…], …} — extra
// top-level fields such as description/runChecks are ignored) into []Op.
// Unknown op codes, restore_* codes (engine-internal, PROJECT_BRIEF_P2 §1.8)
// and malformed field types yield an error wrapping domain.ErrInvalidOp with
// the offending op index. Payload value validation happens in ValidateOp at
// apply time.
func DecodeOps(raw []byte) ([]Op, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("sim: decode ops request: %w: %w", domain.ErrInvalidOp, err)
	}
	opsRaw, ok := top["ops"]
	if !ok {
		return nil, fmt.Errorf("sim: decode ops request: missing %q: %w", "ops", domain.ErrInvalidOp)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(opsRaw, &items); err != nil {
		return nil, fmt.Errorf("sim: decode ops request: %q must be an array: %w: %w",
			"ops", domain.ErrInvalidOp, err)
	}

	ops := make([]Op, 0, len(items))
	for i, item := range items {
		var op Op
		dec := json.NewDecoder(bytes.NewReader(item))
		dec.DisallowUnknownFields() // typos like "linId" fail instead of being ignored
		if err := dec.Decode(&op); err != nil {
			return nil, fmt.Errorf("sim: decode op %d: %w: %w", i, domain.ErrInvalidOp, err)
		}
		if !domain.IsForwardOp(op.Op) {
			return nil, fmt.Errorf("sim: op %d: %q: not a forward op code (restore_* ops are engine-internal): %w",
				i, op.Op, domain.ErrInvalidOp)
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// ValidateOp checks the pure payload rules of one forward op
// (PROJECT_BRIEF_P2 §4 "validation (pre-apply)"): strict field usage (no
// set-but-unused field), non-empty identifiers, status enum, interface name
// regex, and IPv4 quad/prefix range with the both-or-neither rule. It never
// touches the database — entity existence, uniqueness and self-loop checks
// are semantic failures raised during application as *OpError wrapping
// domain.ErrOpFailed. An unknown op code is rejected as well.
func ValidateOp(op Op) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("sim: op %s: %s: %w", op.Op, fmt.Sprintf(format, args...), domain.ErrInvalidOp)
	}

	allowed, ok := opAllowedFields[op.Op]
	if !ok {
		return invalid("unknown op code %q", op.Op)
	}
	if extra := unusedFields(op, allowed...); len(extra) > 0 {
		return invalid("fields %v are not used by this op (allowed: %v)", extra, allowed)
	}

	switch op.Op {
	case domain.OpCreateDevice:
		if op.Code == "" {
			return invalid("code must not be empty")
		}
		if op.Name == "" {
			return invalid("name must not be empty")
		}
	case domain.OpSetDeviceStatus:
		if op.DeviceID == "" {
			return invalid("deviceId must not be empty")
		}
		if err := validateStatus(op.Status); err != nil {
			return err
		}
	case domain.OpDeleteDevice, domain.OpDeleteInterface, domain.OpDeleteLink:
		if err := requireOpID(op); err != nil {
			return err
		}
	case domain.OpCreateInterface:
		if op.DeviceID == "" {
			return invalid("deviceId must not be empty")
		}
		if !interfaceNameRe.MatchString(op.Name) {
			return invalid("interface name %q must match ^[A-Za-z0-9._-]{1,32}$", op.Name)
		}
		if op.Status != "" {
			if err := validateStatus(op.Status); err != nil {
				return err
			}
		}
		if err := validateAddrPrefix(op.IPv4Addr, op.IPv4Prefix); err != nil {
			return err
		}
	case domain.OpSetInterfaceStatus:
		if op.InterfaceID == "" {
			return invalid("interfaceId must not be empty")
		}
		if err := validateStatus(op.Status); err != nil {
			return err
		}
	case domain.OpConfigInterfaceIPv4:
		if op.InterfaceID == "" {
			return invalid("interfaceId must not be empty")
		}
		if err := validateAddrPrefix(op.IPv4Addr, op.IPv4Prefix); err != nil {
			return err
		}
	case domain.OpCreateLink:
		if op.AInterfaceID == "" {
			return invalid("aInterfaceId must not be empty")
		}
		if op.ZInterfaceID == "" {
			return invalid("zInterfaceId must not be empty")
		}
		if op.Status != "" {
			if err := validateStatus(op.Status); err != nil {
				return err
			}
		}
	case domain.OpSetLinkStatus:
		if op.LinkID == "" {
			return invalid("linkId must not be empty")
		}
		if err := validateStatus(op.Status); err != nil {
			return err
		}
	}
	return nil
}

// requireOpID checks that exactly the single id field the delete op needs
// is non-empty (existence is a semantic check at apply time).
func requireOpID(op Op) error {
	switch op.Op {
	case domain.OpDeleteDevice:
		if op.DeviceID == "" {
			return fmt.Errorf("sim: op %s: deviceId must not be empty: %w", op.Op, domain.ErrInvalidOp)
		}
	case domain.OpDeleteInterface:
		if op.InterfaceID == "" {
			return fmt.Errorf("sim: op %s: interfaceId must not be empty: %w", op.Op, domain.ErrInvalidOp)
		}
	case domain.OpDeleteLink:
		if op.LinkID == "" {
			return fmt.Errorf("sim: op %s: linkId must not be empty: %w", op.Op, domain.ErrInvalidOp)
		}
	}
	return nil
}

// unusedFields names the set fields of op that are not part of the given
// allowed set (for strict-field-usage error messages).
func unusedFields(op Op, allowed ...string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}
	var used []string
	if op.DeviceID != "" {
		used = append(used, "deviceId")
	}
	if op.Code != "" {
		used = append(used, "code")
	}
	if op.Name != "" {
		used = append(used, "name")
	}
	if op.Status != "" {
		used = append(used, "status")
	}
	if op.InterfaceID != "" {
		used = append(used, "interfaceId")
	}
	if op.IPv4Addr != nil {
		used = append(used, "ipv4Addr")
	}
	if op.IPv4Prefix != nil {
		used = append(used, "ipv4Prefix")
	}
	if op.LinkID != "" {
		used = append(used, "linkId")
	}
	if op.AInterfaceID != "" {
		used = append(used, "aInterfaceId")
	}
	if op.ZInterfaceID != "" {
		used = append(used, "zInterfaceId")
	}
	if op.Label != "" {
		used = append(used, "label")
	}
	out := make([]string, 0, len(used))
	for _, f := range used {
		if !allowedSet[f] {
			out = append(out, f)
		}
	}
	return out
}

// validateStatus checks that status is a valid EntityStatus value
// ("up"/"down"); an empty status is rejected (it is required for set_* ops
// and validated only when set for create_* ops).
func validateStatus(status string) error {
	if status == "" {
		return fmt.Errorf("sim: status must not be empty: %w", domain.ErrInvalidOp)
	}
	if !domain.EntityStatus(status).IsValid() {
		return fmt.Errorf("sim: invalid status %q (want up or down): %w", status, domain.ErrInvalidOp)
	}
	return nil
}

// validateAddrPrefix enforces the IPv4 rules of create_interface and
// config_interface_ipv4: addr and prefix are both set or both nil; a set
// addr must be a valid dotted quad; a set prefix must be 0..32.
func validateAddrPrefix(addr *string, prefix *int) error {
	if (addr == nil) != (prefix == nil) {
		return fmt.Errorf("sim: ipv4Addr and ipv4Prefix must both be set or both be null: %w", domain.ErrInvalidOp)
	}
	if addr == nil {
		return nil
	}
	if *addr == "" {
		return fmt.Errorf("sim: ipv4Addr must be a dotted quad, not empty: %w", domain.ErrInvalidOp)
	}
	if _, err := parseIPv4(*addr); err != nil {
		return fmt.Errorf("sim: op: %w: %w", domain.ErrInvalidOp, err)
	}
	if *prefix < 0 || *prefix > 32 {
		return fmt.Errorf("sim: ipv4Prefix %d out of range (0..32): %w", *prefix, domain.ErrInvalidOp)
	}
	return nil
}

// parseIPv4 parses a dotted-quad IPv4 address and returns its canonical
// text form (netip normalizes and rejects IPv6 / malformed forms).
func parseIPv4(addr string) (string, error) {
	parsed, err := netip.ParseAddr(addr)
	if err != nil || !parsed.Is4() {
		return "", fmt.Errorf("invalid IPv4 dotted quad %q", addr)
	}
	return parsed.String(), nil
}

// newDecoder returns a strict JSON decoder over raw (unknown fields are
// rejected — used for every op-shaped JSON the engine reads).
func newDecoder(raw []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec
}
