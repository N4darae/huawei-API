package hilink

import (
	"context"

	"github.com/n4darae/huawei-API/src/internal/device"
)

const (
	SimStateNoSIM       = 255
	SimStateCPINError   = 256
	SimStateReady       = 257
	SimStatePINDisabled = 258
	SimStatePINChecking = 259
	SimStatePINRequired = 260
	SimStatePUKRequired = 261
)

func (c *Client) PinStatus(ctx context.Context) (device.SimState, error) {
	var r pinStatusResponse
	if err := c.Get(ctx, PathPinStatus, &r); err != nil {
		return 0, err
	}
	return device.SimState(atoi(r.SimState)), nil
}

func SimStateLocked(s device.SimState) bool {
	switch int(s) {
	case SimStatePINChecking, SimStatePINRequired, SimStatePUKRequired:
		return true
	}
	return false
}

func SimStateUsable(s device.SimState) bool {
	switch int(s) {
	case SimStateReady, SimStatePINDisabled:
		return true
	}
	return false
}
