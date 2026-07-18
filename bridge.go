package foxfire

import (
	"context"
	"fmt"
)

// BridgeInfo is the bridge's own CLIP resource (rtype "bridge"). It is the one
// resource that always exists on every bridge and needs no devices paired to
// appear, which makes it the natural first read after pairing to confirm the
// client is talking to the hub it thinks it is.
//
// Named BridgeInfo rather than Bridge because Bridge is already the discovery
// result type, which is a different thing: that is an address the client dials,
// this is a resource the client reads once connected.
//
// Note what is NOT here: the firmware version. The bridge resource carries the
// bridge ID and time zone only; the software version lives on the owning
// device's product_data (see Device.ProductData.SoftwareVersion). Owner points
// at that device.
type BridgeInfo struct {
	ID       ID     `json:"id"`
	Type     string `json:"type"`
	Owner    Ref    `json:"owner"`
	BridgeID string `json:"bridge_id"`
	TimeZone struct {
		TimeZone string `json:"time_zone"`
	} `json:"time_zone"`
}

type BridgeService struct{ c *Client }

func (s *BridgeService) List(ctx context.Context) ([]BridgeInfo, error) {
	return getMany[BridgeInfo](ctx, s.c, "/resource/bridge")
}

func (s *BridgeService) Get(ctx context.Context, id ID) (BridgeInfo, error) {
	return getOne[BridgeInfo](ctx, s.c, "/resource/bridge", id)
}

// Self returns the single bridge resource. A bridge always reports exactly one
// bridge resource -- itself -- so the common case of "which hub is this and
// what firmware" does not need the caller to index into a slice.
func (s *BridgeService) Self(ctx context.Context) (BridgeInfo, error) {
	bridges, err := s.List(ctx)
	if err != nil {
		return BridgeInfo{}, err
	}
	if len(bridges) == 0 {
		return BridgeInfo{}, fmt.Errorf("%w: bridge reported no bridge resource", ErrNotFound)
	}
	return bridges[0], nil
}

// ZigbeeConnectivity reports whether a device is reachable over the Zigbee
// mesh. This matters more than it looks: a command to an unreachable light
// succeeds at the API level and does nothing physically, so a caller that
// wants to know whether an action took effect has to consult this separately.
// Status is one of "connected", "disconnected", "connectivity_issue", or
// "unidirectional_incoming"; only "connected" means commands will land.
type ZigbeeConnectivity struct {
	ID         ID     `json:"id"`
	Type       string `json:"type"`
	Owner      Ref    `json:"owner"`
	Status     string `json:"status"`
	MACAddress string `json:"mac_address"`
	Channel    struct {
		Status string `json:"status"`
		Value  string `json:"value"`
	} `json:"channel"`
}

// Reachable reports whether the owning device can currently receive commands.
func (z ZigbeeConnectivity) Reachable() bool {
	return z.Status == "connected"
}

type ZigbeeConnectivityService struct{ c *Client }

func (s *ZigbeeConnectivityService) List(ctx context.Context) ([]ZigbeeConnectivity, error) {
	return getMany[ZigbeeConnectivity](ctx, s.c, "/resource/zigbee_connectivity")
}

func (s *ZigbeeConnectivityService) Get(ctx context.Context, id ID) (ZigbeeConnectivity, error) {
	return getOne[ZigbeeConnectivity](ctx, s.c, "/resource/zigbee_connectivity", id)
}
