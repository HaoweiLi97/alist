package thunder_browser

import (
	"strconv"
	"testing"
	"time"
)

func TestDirectLinkExpiration(t *testing.T) {
	now := time.Now()

	t.Run("query timestamp", func(t *testing.T) {
		expiresAt := now.Add(5 * time.Minute).Unix()
		ttl, ok := directLinkExpiration("https://download.example/file?e="+strconv.FormatInt(expiresAt, 10), time.Time{}, now)
		if !ok {
			t.Fatal("directLinkExpiration() did not parse query expiry")
		}
		want := 5*time.Minute - directLinkCacheSafetyMargin
		if delta := ttl - want; delta < -time.Second || delta > time.Second {
			t.Fatalf("ttl = %v, want about %v", ttl, want)
		}
	})

	t.Run("payload expiry takes precedence", func(t *testing.T) {
		expiresAt := now.Add(2 * time.Minute)
		ttl, ok := directLinkExpiration("https://download.example/file?e=1", expiresAt, now)
		if !ok {
			t.Fatal("directLinkExpiration() did not use payload expiry")
		}
		want := 2*time.Minute - directLinkCacheSafetyMargin
		if delta := ttl - want; delta < -time.Second || delta > time.Second {
			t.Fatalf("ttl = %v, want about %v", ttl, want)
		}
	})

	t.Run("expired or near-expiry link", func(t *testing.T) {
		if _, ok := directLinkExpiration("https://download.example/file?e="+strconv.FormatInt(now.Add(20*time.Second).Unix(), 10), time.Time{}, now); ok {
			t.Fatal("directLinkExpiration() cached a near-expiry link")
		}
	})
}
