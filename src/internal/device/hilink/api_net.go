package hilink

import (
	"context"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

func NetworkModeCode(m device.NetMode) (string, bool) {
	switch m {
	case device.NetModeAuto:
		return NetworkModeAuto, true
	case device.NetMode2G:
		return NetworkMode2G, true
	case device.NetMode3G:
		return NetworkMode3G, true
	case device.NetModeLTE:
		return NetworkModeLTE, true
	}
	return "", false
}

func NetModeFromCode(code string) (device.NetMode, bool) {
	switch code {
	case NetworkModeAuto:
		return device.NetModeAuto, true
	case NetworkMode2G:
		return device.NetMode2G, true
	case NetworkMode3G:
		return device.NetMode3G, true
	case NetworkModeLTE:
		return device.NetModeLTE, true
	}
	return "", false
}

func (c *Client) NetMode(ctx context.Context) (device.NetMode, error) {
	var r netModeResponse
	if err := c.Get(ctx, PathNetNetMode, &r); err != nil {
		return "", err
	}
	m, ok := NetModeFromCode(r.NetworkMode)
	if !ok {
		return "", domain.Wrap(domain.ErrInvalid, "hilink: unknown NetworkMode %q", r.NetworkMode)
	}
	return m, nil
}

func (c *Client) SetNetMode(ctx context.Context, m device.NetMode) error {
	code, ok := NetworkModeCode(m)
	if !ok {
		return domain.Wrap(domain.ErrInvalid, "hilink: net mode %q", string(m))
	}
	var cur netModeResponse
	if err := c.Get(ctx, PathNetNetMode, &cur); err != nil {
		return err
	}
	req := netModeRequest{
		NetworkMode: code,
		NetworkBand: cur.NetworkBand,
		LTEBand:     cur.LTEBand,
	}
	if req.NetworkBand == "" {
		req.NetworkBand = NetworkBandAll
	}
	if req.LTEBand == "" {
		req.LTEBand = NetworkLTEAll
	}
	return c.Post(ctx, PathNetNetMode, req, nil)
}

type PLMN struct {
	State     int
	FullName  string
	ShortName string
	Numeric   string
	Rat       string
}

func (c *Client) CurrentPLMN(ctx context.Context) (PLMN, error) {
	return c.plmn(ctx, PathNetCurrentPLMN)
}

func (c *Client) Register(ctx context.Context) (PLMN, error) {
	return c.plmn(ctx, PathNetRegister)
}

func (c *Client) SetRegisterAuto(ctx context.Context) error {
	req := registerRequest{Mode: "0", Plmn: "", Rat: ""}
	return c.Post(ctx, PathNetRegister, req, nil)
}

func (c *Client) SetRegister(ctx context.Context, plmn, rat string) error {
	req := registerRequest{Mode: "1", Plmn: plmn, Rat: rat}
	return c.Post(ctx, PathNetRegister, req, nil)
}

func (c *Client) plmn(ctx context.Context, path string) (PLMN, error) {
	var r plmnResponse
	if err := c.Get(ctx, path, &r); err != nil {
		return PLMN{}, err
	}
	return PLMN{
		State:     atoi(r.State),
		FullName:  r.FullName,
		ShortName: r.ShortName,
		Numeric:   r.Numeric,
		Rat:       r.Rat,
	}, nil
}
