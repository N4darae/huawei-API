package hilink

import (
	"context"
	"errors"

	"github.com/n4darae/huawei-API/src/internal/domain"
)

func (c *Client) LoginRequired(ctx context.Context) (bool, error) {
	var r hilinkLoginResponse
	err := c.Get(ctx, PathHiLinkLogin, &r)
	if err == nil {
		return isSet(r.HilinkLogin), nil
	}
	if errors.Is(err, domain.ErrNeedLogin) || errors.Is(err, domain.ErrLoginRequired) {
		return true, nil
	}
	if errors.Is(err, domain.ErrUnsupported) {
		return false, nil
	}
	return false, err
}
