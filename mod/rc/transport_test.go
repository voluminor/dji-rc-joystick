package rc

import (
	"testing"
	"time"
)

func TestTransportSurvivesInputFailSafeWindow(t *testing.T) {
	now := time.Now()
	live := &readerLivenessObj{stream: new(streamLivenessObj)}
	live.connected(now)
	live.observed(now)
	if live.expired(now.Add(frameIdleTimeout + 100*time.Millisecond)) {
		t.Fatal("input timeout destroyed recovering transport")
	}
	if !live.expired(now.Add(transportIdleTimeout)) {
		t.Fatal("dead transport never expires")
	}
	live.observed(now.Add(2 * time.Second))
	if live.expired(now.Add(transportIdleTimeout)) {
		t.Fatal("recovered stream still expired")
	}
}
