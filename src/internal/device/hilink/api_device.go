package hilink

import (
	"context"

	"github.com/n4darae/huawei-API/src/internal/device"
)

func (c *Client) Information(ctx context.Context) (device.Info, error) {
	var r infoResponse
	if err := c.Get(ctx, PathDeviceInformation, &r); err != nil {
		return device.Info{}, err
	}
	return device.Info{
		DeviceName:      r.DeviceName,
		SerialNumber:    r.SerialNumber,
		IMEI:            r.IMEI,
		IMSI:            r.IMSI,
		ICCID:           r.ICCID,
		MSISDN:          r.MSISDN,
		HardwareVersion: r.HardwareVersion,
		SoftwareVersion: r.SoftwareVersion,
		WebUIVersion:    r.WebUIVersion,
		MacAddress1:     r.MacAddress1,
		MacAddress2:     r.MacAddress2,
		ProductFamily:   r.ProductFamily,
		Classify:        r.Classify,
		Uptime:          atoi64(r.Uptime),
		WanIPAddress:    parseAddr(r.WanIPAddress),
		WanDNSAddress:   parseAddrList(r.WanDNSAddress),
	}, nil
}

func (c *Client) Signal(ctx context.Context) (device.Signal, error) {
	var r signalResponse
	if err := c.Get(ctx, PathDeviceSignal, &r); err != nil {
		return device.Signal{}, err
	}
	return device.Signal{
		RSSI:   suffixInt(r.RSSI),
		RSRP:   suffixInt(r.RSRP),
		RSRQ:   suffixInt(r.RSRQ),
		SINR:   suffixInt(r.SINR),
		Band:   r.Band,
		CellID: r.CellID,
		PLMN:   r.PLMN,
		Mode:   r.Mode,
	}, nil
}

func (c *Client) Status(ctx context.Context) (device.Status, error) {
	var r statusResponse
	if err := c.Get(ctx, PathMonitoringStatus, &r); err != nil {
		return device.Status{}, err
	}
	return device.Status{
		ConnectionStatus:     device.ConnStatus(atoi(r.ConnectionStatus)),
		SimStatus:            atoi(r.SimStatus),
		SignalStrength:       atoi(r.SignalStrength),
		SignalIcon:           atoi(r.SignalIcon),
		MaxSignal:            atoi(r.MaxSignal),
		CurrentNetworkType:   atoi(r.CurrentNetworkType),
		CurrentNetworkTypeEx: atoi(r.CurrentNetworkTypeEx),
		ServiceStatus:        atoi(r.ServiceStatus),
		RoamingStatus:        atoi(r.RoamingStatus),
		WanIP:                parseAddr(r.WanIPAddress),
	}, nil
}

func (c *Client) Reboot(ctx context.Context) error {
	req := controlRequest{Control: itoa(ControlReboot)}
	return c.Post(ctx, PathDeviceControl, req, nil)
}
