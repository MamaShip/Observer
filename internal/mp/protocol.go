// Package mp is the WeChat Official Account entry (plan.md §6.7): WeChat
// POSTs what users send to our subscription account, and each request is
// answered with one passive reply. Plain-text mode over HTTPS: messages are
// authenticated by the signature, not encrypted.
package mp

import (
	"crypto/sha1" //nolint:gosec // the signature scheme is WeChat's, not ours to choose
	"crypto/subtle"
	"encoding/hex"
	"encoding/xml"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxReplyBytes is WeChat's limit on a text reply's content.
const maxReplyBytes = 2048

// clockSkew bounds how far a request's timestamp may be from ours.
const clockSkew = 10 * time.Minute

// Message is what WeChat sends: a user message or an event.
type Message struct {
	ToUserName   string `xml:"ToUserName"`   // our account (gh_…)
	FromUserName string `xml:"FromUserName"` // the user's OpenID
	CreateTime   int64  `xml:"CreateTime"`
	MsgType      string `xml:"MsgType"`
	Content      string `xml:"Content"` // text
	MsgID        int64  `xml:"MsgId"`
	Event        string `xml:"Event"` // event: subscribe, unsubscribe, …
	Title        string `xml:"Title"` // link
	URL          string `xml:"Url"`   // link
}

func parseMessage(body []byte) (Message, error) {
	var m Message
	if err := xml.Unmarshal(body, &m); err != nil {
		return Message{}, err
	}
	m.MsgType, m.Event = strings.ToLower(strings.TrimSpace(m.MsgType)), strings.ToLower(strings.TrimSpace(m.Event))
	return m, nil
}

func (m Message) isUnsubscribe() bool {
	return m.MsgType == "event" && m.Event == "unsubscribe"
}

// dedupKey identifies a message across WeChat's retries.
func (m Message) dedupKey() string {
	if m.MsgID != 0 {
		return "m" + m.FromUserName + ":" + strconv.FormatInt(m.MsgID, 10)
	}
	return "e" + m.FromUserName + ":" + strconv.FormatInt(m.CreateTime, 10) + ":" + m.Event
}

// signature is WeChat's request signature: sha1 of the sorted, concatenated
// token, timestamp and nonce.
func signature(token, timestamp, nonce string) string {
	parts := []string{token, timestamp, nonce}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, ""))) //nolint:gosec // see import
	return hex.EncodeToString(h[:])
}

// verify checks a request's signature and that its timestamp is recent.
func verify(token, sig, timestamp, nonce string, now time.Time) bool {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || token == "" || nonce == "" {
		return false
	}
	if d := now.Sub(time.Unix(ts, 0)); d > clockSkew || d < -clockSkew {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(signature(token, timestamp, nonce)), []byte(strings.ToLower(sig))) == 1
}

type cdata struct {
	Text string `xml:",cdata"`
}

type textReply struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   cdata
	FromUserName cdata
	CreateTime   int64
	MsgType      cdata
	Content      cdata
}

// encodeText builds a passive text reply. encoding/xml splits any "]]>" in
// the content across CDATA sections.
func encodeText(to, from, content string, now time.Time) []byte {
	b, err := xml.Marshal(textReply{
		ToUserName: cdata{to}, FromUserName: cdata{from}, CreateTime: now.Unix(),
		MsgType: cdata{"text"}, Content: cdata{content},
	})
	if err != nil { // only strings and an int: cannot fail
		panic(err)
	}
	return b
}
