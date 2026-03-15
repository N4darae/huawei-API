package hilink

import (
	"context"

	"github.com/n4darae/huawei-API/src/internal/device"
)

func (c *Client) DHCPSettings(ctx context.Context) (device.DHCPSettings, error) {
	var r dhcpResponse
	if err := c.Get(ctx, PathDHCPSettings, &r); err != nil {
		return device.DHCPSettings{}, err
	}
	lease := atoi(r.DHCPLeaseTime)
	if lease == 0 {
		lease = DefaultDHCPLeaseTime
	}
	return device.DHCPSettings{
		DHCPIPAddress:      parseAddr(r.DHCPIPAddress),
		DHCPLanNetmask:     netmaskAddr(parseAddr(r.DHCPLanNetmask)),
		DHCPStatus:         isSet(r.DHCPStatus),
		DHCPStartIPAddress: parseAddr(r.DHCPStartIPAddress),
		DHCPEndIPAddress:   parseAddr(r.DHCPEndIPAddress),
		DHCPLeaseTime:      lease,
		DNSStatus:          isSet(r.DNSStatus),
		PrimaryDNS:         parseAddr(r.PrimaryDNS),
		SecondaryDNS:       parseAddr(r.SecondaryDNS),
	}, nil
}

func (c *Client) SetDHCPSettings(ctx context.Context, s device.DHCPSettings) error {
	req := DHCPRequestFrom(s)
	return c.Post(ctx, PathDHCPSettings, req, nil)
}

func DHCPRequestFrom(s device.DHCPSettings) any {
	lease := s.DHCPLeaseTime
	if lease <= 0 {
		lease = DefaultDHCPLeaseTime
	}
	return dhcpRequest{
		DHCPIPAddress:      addrString(s.DHCPIPAddress),
		DHCPLanNetmask:     addrString(netmaskAddr(s.DHCPLanNetmask)),
		DHCPStatus:         bit(s.DHCPStatus),
		DHCPStartIPAddress: addrString(s.DHCPStartIPAddress),
		DHCPEndIPAddress:   addrString(s.DHCPEndIPAddress),
		DHCPLeaseTime:      itoa(lease),
		DNSStatus:          bit(s.DNSStatus),
		PrimaryDNS:         addrString(s.PrimaryDNS),
		SecondaryDNS:       addrString(s.SecondaryDNS),
		ShowDNSSetting:     bit(s.DNSStatus),
	}
}
