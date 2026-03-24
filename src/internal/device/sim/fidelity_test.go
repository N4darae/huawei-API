package sim

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/n4darae/huawei-API/src/internal/device/hilink"
)

func elementSequence(t *testing.T, body []byte) []string {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(body))
	var out []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok {
			out = append(out, se.Name.Local)
		}
	}
	return out
}

func rawGet(t *testing.T, f *Farm, path string) (*http.Response, []byte) {
	t.Helper()
	base := f.BaseURL(slot1)
	resp, err := http.Get(base + "/api/" + hilink.PathSesTokInfo)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var info struct {
		SesInfo string `xml:"SesInfo"`
		TokInfo string `xml:"TokInfo"`
	}
	if err := xml.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, base+"/api/"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(hilink.HeaderCookie, info.SesInfo)
	req.Header.Set(hilink.HeaderToken, strings.Split(info.TokInfo, hilink.TokenSeparator)[0])
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func TestSimServesTheCapturedElementSequence(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	cases := []struct {
		path    string
		fixture string
	}{
		{hilink.PathDeviceInformation, "device_information.xml"},
		{hilink.PathDeviceSignal, "device_signal.xml"},
		{hilink.PathMonitoringStatus, "monitoring_status_wanip.xml"},
		{hilink.PathMonitoringTraffic, "monitoring_traffic_statistics.xml"},
		{hilink.PathMonitoringMonthStats, "monitoring_month_statistics.xml"},
		{hilink.PathDialupConnection, "dialup_connection.xml"},
		{hilink.PathDialupDataSwitch, "dialup_mobile_dataswitch.xml"},
		{hilink.PathNetCurrentPLMN, "net_current_plmn.xml"},
		{hilink.PathNetRegister, "net_register.xml"},
		{hilink.PathNetNetMode, "net_net_mode.xml"},
		{hilink.PathPinStatus, "pin_status.xml"},
		{hilink.PathDHCPSettings, "dhcp_settings.xml"},
		{hilink.PathHiLinkLogin, "user_hilink_login.xml"},
	}
	for _, c := range cases {
		resp, body := rawGet(t, f, c.path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", c.path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != hilink.ContentTypeResponse {
			t.Errorf("%s: Content-Type %q, the firmware always answers text/html", c.path, ct)
		}
		want := elementSequence(t, hilink.Fixture(c.fixture))
		got := elementSequence(t, body)
		if len(want) != len(got) {
			t.Errorf("%s: %d elements served, fixture has %d\n got %v\nwant %v",
				c.path, len(got), len(want), got, want)
			continue
		}
		for i := range want {
			if want[i] != got[i] {
				t.Errorf("%s: element %d is %q, fixture has %q", c.path, i, got[i], want[i])
			}
		}
	}
}

func TestSimKeepsMaxIdelTimeMisspellingOnTheWire(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	_, body := rawGet(t, f, hilink.PathDialupConnection)
	s := string(body)
	if !strings.Contains(s, "<MaxIdelTime>300</MaxIdelTime>") {
		t.Fatalf("sim body = %q", s)
	}
	if strings.Contains(s, "MaxIdleTime") {
		t.Fatalf("sim corrected the firmware misspelling: %q", s)
	}
}

func TestSimErrorsAre200TextHTML(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	resp, body := rawGet(t, f, "wlan/basic-settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an API error must still be HTTP 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != hilink.ContentTypeResponse {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !bytes.Equal(body, hilink.Fixture("error_100002.xml")) {
		t.Fatalf("the sim must serve the captured error fixture verbatim, got %q", body)
	}
}

func TestSimSesTokInfoMatchesCapturedShape(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	resp, err := http.Get(f.BaseURL(slot1) + "/api/" + hilink.PathSesTokInfo)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	want := elementSequence(t, hilink.Fixture("webserver_sestokinfo.xml"))
	got := elementSequence(t, body)
	if len(want) != len(got) {
		t.Fatalf("got %v, fixture %v", got, want)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("element %d = %q, fixture %q", i, got[i], want[i])
		}
	}
	if resp.Header.Get("Set-Cookie") == "" {
		t.Error("SesTokInfo must set the SessionID cookie")
	}
	var info struct {
		SesInfo string `xml:"SesInfo"`
		TokInfo string `xml:"TokInfo"`
	}
	if err := xml.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.SesInfo, hilink.CookieName+"=") {
		t.Errorf("SesInfo = %q", info.SesInfo)
	}
	if n := len(strings.Split(info.TokInfo, hilink.TokenSeparator)); n != TokenBatch {
		t.Errorf("TokInfo carries %d tokens, want a batch of %d", n, TokenBatch)
	}
}

func TestSimSMSListMatchesCapturedShape(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	d := f.Device(slot1)
	d.AddMessage(Message{Phone: "+48616673870", Content: "one", SmsType: 1})
	d.AddMessage(Message{Phone: "3350", Content: "two", SmsType: 1})

	c := newClient(t, f, slot1)
	if _, _, err := c.SMSList(t.Context(), 1, 1, 20); err != nil {
		t.Fatal(err)
	}

	want := elementSequence(t, hilink.Fixture("sms_sms_list.xml"))
	body := simSMSBody(t, f)
	got := elementSequence(t, body)
	if len(want) != len(got) {
		t.Fatalf("got %d elements, fixture has %d\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("element %d = %q, fixture %q", i, got[i], want[i])
		}
	}
}

func simSMSBody(t *testing.T, f *Farm) []byte {
	t.Helper()
	base := f.BaseURL(slot1)
	resp, err := http.Get(base + "/api/" + hilink.PathSesTokInfo)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var info struct {
		SesInfo string `xml:"SesInfo"`
		TokInfo string `xml:"TokInfo"`
	}
	if err := xml.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}
	payload := hilink.XMLProlog + "<request><PageIndex>1</PageIndex><ReadCount>20</ReadCount>" +
		"<BoxType>1</BoxType><SortType>0</SortType><Ascending>0</Ascending>" +
		"<UnreadPreferred>1</UnreadPreferred></request>"
	req, err := http.NewRequest(http.MethodPost, base+"/api/"+hilink.PathSMSList, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(hilink.HeaderCookie, info.SesInfo)
	req.Header.Set(hilink.HeaderToken, strings.Split(info.TokInfo, hilink.TokenSeparator)[0])
	req.Header.Set("Content-Type", hilink.ContentTypeXML)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return out
}

func TestSimSetResponseIsTheCapturedOK(t *testing.T) {
	f := newFarm(t, FarmOptions{})
	c := newClient(t, f, slot1)
	if err := c.DataSwitch(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(hilink.Fixture("response_ok.xml"), []byte("<response>OK</response>")) {
		t.Fatal("the captured OK response changed shape")
	}
}
