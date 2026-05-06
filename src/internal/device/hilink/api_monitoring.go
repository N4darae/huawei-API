package hilink

import (
	"context"

	"github.com/n4darae/huawei-API/src/internal/device"
)

func (c *Client) Traffic(ctx context.Context) (device.Traffic, error) {
	var r trafficResponse
	if err := c.Get(ctx, PathMonitoringTraffic, &r); err != nil {
		return device.Traffic{}, err
	}
	return device.Traffic{
		CurrentConnectTime:  atoi64(r.CurrentConnectTime),
		CurrentUpload:       atoi64(r.CurrentUpload),
		CurrentDownload:     atoi64(r.CurrentDownload),
		CurrentUploadRate:   atoi64(r.CurrentUploadRate),
		CurrentDownloadRate: atoi64(r.CurrentDownloadRate),
		TotalUpload:         atoi64(r.TotalUpload),
		TotalDownload:       atoi64(r.TotalDownload),
		TotalConnectTime:    atoi64(r.TotalConnectTime),
	}, nil
}

func (c *Client) MonthStats(ctx context.Context) (device.MonthStats, error) {
	var r monthStatsResponse
	if err := c.Get(ctx, PathMonitoringMonthStats, &r); err != nil {
		return device.MonthStats{}, err
	}
	return device.MonthStats{
		CurrentMonthUpload:   atoi64(r.CurrentMonthUpload),
		CurrentMonthDownload: atoi64(r.CurrentMonthDownload),
		MonthDuration:        atoi64(r.MonthDuration),
		MonthLastClearTime:   r.MonthLastClearTime,
	}, nil
}
