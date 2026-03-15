package hilink

import (
	"context"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func (c *Client) DataSwitch(ctx context.Context, on bool) error {
	req := dataSwitchRequest{DataSwitch: bit(on)}
	return c.Post(ctx, PathDialupDataSwitch, req, nil)
}

func (c *Client) DataSwitchState(ctx context.Context) (bool, error) {
	var r dataSwitchResponse
	if err := c.Get(ctx, PathDialupDataSwitch, &r); err != nil {
		return false, err
	}
	return isSet(r.DataSwitch), nil
}

func (c *Client) GetMaxIdleTime(ctx context.Context) (int, error) {
	r, err := c.connection(ctx)
	if err != nil {
		return 0, err
	}
	return atoi(r.MaxIdelTime), nil
}

func (c *Client) SetMaxIdleTime(ctx context.Context, seconds int) error {
	if seconds < 0 {
		return domain.Wrap(domain.ErrInvalid, "hilink: max idle time %d", seconds)
	}
	r, err := c.connection(ctx)
	if err != nil {
		return err
	}
	req := connectionRequest{
		RoamAutoConnectEnable: r.RoamAutoConnectEnable,
		MaxIdelTime:           itoa(seconds),
		ConnectMode:           r.ConnectMode,
		MTU:                   r.MTU,
		AutoDialSwitch:        r.AutoDialSwitch,
		PdpAlwaysOn:           r.PdpAlwaysOn,
	}
	if req.MTU == "" {
		req.MTU = "1500"
	}
	return c.Post(ctx, PathDialupConnection, req, nil)
}

func (c *Client) connection(ctx context.Context) (connectionResponse, error) {
	var r connectionResponse
	if err := c.Get(ctx, PathDialupConnection, &r); err != nil {
		return connectionResponse{}, err
	}
	if r.MaxIdelTime == "" {
		r.MaxIdelTime = itoa(device.MaxIdleTimeDefault)
	}
	return r, nil
}
