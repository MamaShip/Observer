package mp

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Simulate sends m to a running service as WeChat would (signed, to
// base+Path) and returns the reply's text, "" for none. It is for
// observer mpsim: trying the entry without the WeChat platform.
func Simulate(ctx context.Context, c *http.Client, base, token string, m Message) (string, error) {
	now := time.Now()
	if m.CreateTime == 0 {
		m.CreateTime = now.Unix()
	}
	if m.MsgType != "event" && m.MsgID == 0 {
		m.MsgID = now.UnixNano()
	}
	body, err := xml.Marshal(struct {
		XMLName xml.Name `xml:"xml"`
		Message
	}{Message: m})
	if err != nil {
		return "", err
	}
	ts, nonce := strconv.FormatInt(now.Unix(), 10), strconv.FormatInt(now.UnixNano(), 36)
	q := url.Values{"signature": {signature(token, ts, nonce)}, "timestamp": {ts}, "nonce": {nonce}, "openid": {m.FromUserName}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+Path+"?"+q.Encode(), strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "text/xml")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if string(b) == "success" {
		return "", nil
	}
	var r struct{ Content string }
	if err := xml.Unmarshal(b, &r); err != nil {
		return "", fmt.Errorf("reply %q: %w", b, err)
	}
	return r.Content, nil
}
