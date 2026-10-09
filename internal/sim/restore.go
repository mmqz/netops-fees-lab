package sim

import (
	"encoding/json"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// DeviceSnap is the captured pre-state of a virtual device for the
// restore_device inverse of delete_device: the device row plus ids and all
// business fields (timestamps are not business state; restored rows get
// fresh ones).
type DeviceSnap struct {
	ID         string `json:"id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	ResourceID string `json:"resourceId"` // "" = no lineage resource
}

// InterfaceSnap is the captured pre-state of a simulated interface for the
// restore inverses: id, owning device and all business fields. Nil pointers
// mean "unconfigured" (SQL NULL).
type InterfaceSnap struct {
	ID         string  `json:"id"`
	DeviceID   string  `json:"deviceId"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	IPv4Addr   *string `json:"ipv4Addr"`
	IPv4Prefix *int    `json:"ipv4Prefix"`
}

// LinkSnap is the captured pre-state of a simulated link for the restore
// inverses: id, both endpoint interfaces and all business fields.
type LinkSnap struct {
	ID           string `json:"id"`
	AInterfaceID string `json:"aInterfaceId"`
	ZInterfaceID string `json:"zInterfaceId"`
	Status       string `json:"status"`
	Label        string `json:"label"`
	ResourceID   string `json:"resourceId"`
}

// RestoreDeviceOp is the engine-internal inverse of delete_device: it
// re-inserts the captured device, its interfaces and the links that touched
// them, keeping the ORIGINAL ids (FKs stay valid because undo applies
// inverse ops in reverse seq order). Its own inverse is
// delete_device {deviceId}.
type RestoreDeviceOp struct {
	Op         string          `json:"op"` // domain.OpRestoreDevice
	Device     DeviceSnap      `json:"device"`
	Interfaces []InterfaceSnap `json:"interfaces"`
	Links      []LinkSnap      `json:"links"`
}

// RestoreInterfaceOp is the engine-internal inverse of delete_interface: it
// re-inserts the captured interface and the links that touched it with
// their original ids. Its own inverse is delete_interface {interfaceId}.
type RestoreInterfaceOp struct {
	Op        string        `json:"op"` // domain.OpRestoreInterface
	Interface InterfaceSnap `json:"interface"`
	Links     []LinkSnap    `json:"links"`
}

// RestoreLinkOp is the engine-internal inverse of delete_link: it
// re-inserts the captured link row with its original id. Its own inverse is
// delete_link {linkId}.
type RestoreLinkOp struct {
	Op   string   `json:"op"` // domain.OpRestoreLink
	Link LinkSnap `json:"link"`
}

// code returns the op code (the discriminant of the payload).
func (r *RestoreDeviceOp) code() string    { return domain.OpRestoreDevice }
func (r *RestoreInterfaceOp) code() string { return domain.OpRestoreInterface }
func (r *RestoreLinkOp) code() string      { return domain.OpRestoreLink }

// opJSON marshals the restore op into its canonical typed JSON (stored as
// changeset_ops.op_json of undo changesets).
func (r *RestoreDeviceOp) opJSON() (string, error) {
	r.Op = domain.OpRestoreDevice
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("sim: marshal restore_device: %w", err)
	}
	return string(b), nil
}

func (r *RestoreInterfaceOp) opJSON() (string, error) {
	r.Op = domain.OpRestoreInterface
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("sim: marshal restore_interface: %w", err)
	}
	return string(b), nil
}

func (r *RestoreLinkOp) opJSON() (string, error) {
	r.Op = domain.OpRestoreLink
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("sim: marshal restore_link: %w", err)
	}
	return string(b), nil
}
