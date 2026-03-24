package hilink

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device"
	"github.com/n4darae/huawei-API/src/internal/domain"
)

const (
	SMSDateLayout    = "2006-01-02 15:04:05"
	SMSSortByDate    = "0"
	SMSDefaultPage   = 1
	SMSDefaultSize   = 20
	SMSMaxSize       = 50
	SMSUnreadPrefer  = "1"
	SMSAscendingNone = "0"
)

func ParseSMSDate(s string) int64 {
	t, err := time.ParseInLocation(SMSDateLayout, strings.TrimSpace(s), time.Local)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func (c *Client) SMSList(ctx context.Context, box device.SMSBox, page, size int) ([]device.SMS, int, error) {
	if !box.Valid() {
		return nil, 0, domain.Wrap(domain.ErrInvalid, "hilink: sms box %d", int(box))
	}
	if page <= 0 {
		page = SMSDefaultPage
	}
	if size <= 0 {
		size = SMSDefaultSize
	}
	if size > SMSMaxSize {
		size = SMSMaxSize
	}
	req := smsListRequest{
		PageIndex:       itoa(page),
		ReadCount:       itoa(size),
		BoxType:         itoa(int(box)),
		SortType:        SMSSortByDate,
		Ascending:       SMSAscendingNone,
		UnreadPreferred: SMSUnreadPrefer,
	}
	var r smsListResponse
	if err := c.Post(ctx, PathSMSList, req, &r); err != nil {
		return nil, 0, err
	}
	out := make([]device.SMS, 0, len(r.Messages.Messages))
	for _, m := range r.Messages.Messages {
		out = append(out, smsFrom(m, box))
	}
	total := atoi(r.Count)
	if total == 0 {
		total = len(out)
	}
	return out, total, nil
}

func smsFrom(m smsMessage, box device.SMSBox) device.SMS {
	smsType := atoi(m.SmsType)
	return device.SMS{
		Index:      atoi64(m.Index),
		Phone:      m.Phone,
		Content:    m.Content,
		Date:       ParseSMSDate(m.Date),
		Box:        box,
		Read:       atoi(m.Smstat) != SMSStatusNew,
		SmsType:    smsType,
		IsFragment: smsType == device.SMSTypeFragment,
	}
}

func (c *Client) SMSSend(ctx context.Context, to []string, body string) error {
	if len(to) == 0 {
		return domain.Wrap(domain.ErrInvalid, "hilink: send-sms without recipient")
	}
	phones := make([]string, 0, len(to))
	for _, p := range to {
		p = strings.TrimSpace(p)
		if p != "" {
			phones = append(phones, p)
		}
	}
	if len(phones) == 0 {
		return domain.Wrap(domain.ErrInvalid, "hilink: send-sms without recipient")
	}
	req := smsSendRequest{
		Index:    strconv.Itoa(SMSSendIndexNew),
		Phones:   smsPhones{Phone: phones},
		Sca:      "",
		Content:  body,
		Length:   itoa(len([]rune(body))),
		Reserved: itoa(SMSSendReserved),
		Date:     time.Now().Format(SMSDateLayout),
	}
	return c.Post(ctx, PathSMSSend, req, nil)
}

func (c *Client) SMSDelete(ctx context.Context, index int64) error {
	req := smsIndexRequest{Index: strconv.FormatInt(index, 10)}
	return c.Post(ctx, PathSMSDelete, req, nil)
}

func (c *Client) SMSSetRead(ctx context.Context, index int64) error {
	req := smsIndexRequest{Index: strconv.FormatInt(index, 10)}
	return c.Post(ctx, PathSMSSetRead, req, nil)
}
