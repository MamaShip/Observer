package wechat

import (
	"errors"
	"testing"
)

func TestParseLink(t *testing.T) {
	const biz, mid, idx, sn = "MzU1NDQ2OTg1NQ==", "2247501049", "1", "4ba683d7a4184285569a2d6a29dc951b"
	long := "https://mp.weixin.qq.com/s?__biz=MzU1NDQ2OTg1NQ==&mid=2247501049&idx=1&sn=4ba683d7a4184285569a2d6a29dc951b"

	tests := []struct {
		name      string
		in        string
		wantErr   error
		wantKind  LinkKind
		wantKey   string
		wantFetch string
	}{
		{name: "short", in: "https://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg",
			wantKind: LinkShort, wantKey: "https://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg",
			wantFetch: "https://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg"},
		{name: "short with tracking, http, trailing slash and spaces",
			in:       "  http://MP.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg/?scene=1&from=singlemessage#rd ",
			wantKind: LinkShort, wantKey: "https://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg",
			wantFetch: "https://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg"},
		{name: "long keeps chksm for fetching only",
			in: "https://mp.weixin.qq.com/s?sn=" + sn + "&mid=" + mid + "&__biz=" + biz + "&idx=" + idx +
				"&chksm=fa2cc7fdcd5b4eeb&mpshare=1&scene=1&pass_ticket=abc#rd",
			wantKind: LinkLong, wantKey: long, wantFetch: long + "&chksm=fa2cc7fdcd5b4eeb"},
		{name: "long with encoded biz", in: "https://mp.weixin.qq.com/s?__biz=MzU1NDQ2OTg1NQ%3D%3D&mid=" + mid + "&idx=1&sn=" + sn,
			wantKind: LinkLong, wantKey: long, wantFetch: long},
		{name: "long with garbage chksm drops it", in: long + "&chksm=<script>",
			wantKind: LinkLong, wantKey: long, wantFetch: long},
		{name: "temporary link", in: "https://mp.weixin.qq.com/s?src=11&timestamp=1700000000&ver=4870&signature=abc",
			wantErr: ErrTemporaryLink},
		{name: "long missing sn", in: "https://mp.weixin.qq.com/s?__biz=" + biz + "&mid=" + mid + "&idx=1", wantErr: ErrMalformed},
		{name: "other host", in: "https://evil.example.com/s/YPak1xLfYK-BOjk-OYbAKg", wantErr: ErrNotArticle},
		{name: "lookalike host", in: "https://mp.weixin.qq.com.evil.com/s/YPak1xLfYK-BOjk-OYbAKg", wantErr: ErrNotArticle},
		{name: "userinfo", in: "https://x@mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg", wantErr: ErrMalformed},
		{name: "odd port", in: "https://mp.weixin.qq.com:8443/s/YPak1xLfYK-BOjk-OYbAKg", wantErr: ErrMalformed},
		{name: "other path", in: "https://mp.weixin.qq.com/mp/profile_ext?action=home", wantErr: ErrNotArticle},
		{name: "bad token", in: "https://mp.weixin.qq.com/s/abc", wantErr: ErrMalformed},
		{name: "not a url", in: "hello", wantErr: ErrNotArticle},
		{name: "ftp", in: "ftp://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg", wantErr: ErrNotArticle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLink(tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.Kind != tt.wantKind || got.Key != tt.wantKey || got.FetchURL != tt.wantFetch {
				t.Fatalf("got kind=%s key=%s fetch=%s\nwant kind=%s key=%s fetch=%s",
					got.Kind, got.Key, got.FetchURL, tt.wantKind, tt.wantKey, tt.wantFetch)
			}
		})
	}
}

func TestServiceFetchURL(t *testing.T) {
	long := "https://mp.weixin.qq.com/s?__biz=MzU1NDQ2OTg1NQ==&mid=2247501049&idx=1&sn=4ba683d7a4184285569a2d6a29dc951b"
	for in, want := range map[string]string{
		long:                     long + "&chksm=" + PlaceholderChksm,
		long + "&chksm=abcdef12": long + "&chksm=abcdef12",
		"https://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg": "https://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg",
	} {
		l, err := ParseLink(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := l.ServiceFetchURL(); got != want {
			t.Errorf("ServiceFetchURL(%s) = %s, want %s", in, got, want)
		}
	}
}
