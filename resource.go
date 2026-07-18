package foxfire

import "encoding/json"

// ID is a CLIP v2 resource identifier. The bridge emits RFC 4122 UUIDs as
// strings; we keep them as strings because we never need to inspect the bits
// and parsing would only add a dependency and a class of error.
type ID string

// ResourceType enumerates the rtype values the bridge uses in references.
type ResourceType string

const (
	TypeDevice        ResourceType = "device"
	TypeBridge        ResourceType = "bridge"
	TypeLight         ResourceType = "light"
	TypeGroupedLight  ResourceType = "grouped_light"
	TypeRoom          ResourceType = "room"
	TypeZone          ResourceType = "zone"
	TypeScene         ResourceType = "scene"
	TypeMotion        ResourceType = "motion"
	TypeTemperature   ResourceType = "temperature"
	TypeLightLevel    ResourceType = "light_level"
	TypeButton        ResourceType = "button"
	TypeDevicePower   ResourceType = "device_power"
	TypeZigbeeConnect ResourceType = "zigbee_connectivity"
)

// Ref is a typed pointer to another resource. The v2 model is a graph: a
// device owns services, a room references devices, a grouped_light is the
// service through which a room is actuated.
type Ref struct {
	RID   ID           `json:"rid"`
	RType ResourceType `json:"rtype"`
}

// Metadata is the user-facing name and archetype carried by most resources.
type Metadata struct {
	Name      string `json:"name"`
	Archetype string `json:"archetype,omitempty"`
}

// resourceEnvelope is the uniform wrapper the bridge returns for every GET.
// Errors is populated even on 200 responses for partial failures, which is
// why it is checked on every call rather than only on non-2xx.
type resourceEnvelope[T any] struct {
	Errors []apiError      `json:"errors"`
	Data   []T             `json:"data"`
	Raw    json.RawMessage `json:"-"`
}

// updateEnvelope is returned from PUT and POST. Data contains references to
// the touched resources rather than their new state; the new state arrives
// on the event stream.
type updateEnvelope struct {
	Errors []apiError `json:"errors"`
	Data   []Ref      `json:"data"`
}
