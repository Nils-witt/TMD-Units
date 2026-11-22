package servicebus

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestBrokerRoundTrip runs a provider and a consumer against a real broker.
// It runs only with TEST_AMQP_URL, e.g. amqp://guest:guest@localhost:5672/.
func TestBrokerRoundTrip(t *testing.T) {
	t.Parallel()

	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL is unset")
	}

	// A unique service name keeps parallel runs on one broker apart.
	service := "test" + strconv.FormatInt(time.Now().UnixNano(), 36)

	provider, err := New(Options{URL: url, Secret: testSecret, Name: "provider", Self: Announcement{Service: service, InstanceID: "p1", URL: "http://provider"}})
	if err != nil {
		t.Fatal(err)
	}

	provider.ServeTokens(func(_ context.Context, req TokenRequest) (string, time.Time, error) {
		if req.Client != testClient {
			return "", time.Time{}, errors.New("not allowed")
		}

		return "asv_" + req.ClientInstance, time.Now().Add(time.Hour), nil
	})

	consumer, err := New(Options{URL: url, Secret: testSecret, Name: "consumer", Self: Announcement{Service: testClient, InstanceID: "c1"}})
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu   sync.Mutex
		seen []Announcement
	)

	consumer.Watch(service, func(a Announcement) {
		mu.Lock()
		defer mu.Unlock()

		seen = append(seen, a)
	})

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go provider.Run(ctx)

	waitUntil(t, provider.Connected)

	go consumer.Run(ctx)

	// The consumer's discover makes the provider announce at once.
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(seen) > 0
	})

	mu.Lock()
	a := seen[0]
	mu.Unlock()

	if a.URL != "http://provider" || !a.IssuesTokens {
		t.Errorf("announcement = %+v", a)
	}

	token, expires, err := consumer.RequestToken(ctx, service)
	if err != nil || token != "asv_c1" || expires.IsZero() {
		t.Fatalf("RequestToken = %q, %v, %v", token, expires, err)
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}

		time.Sleep(20 * time.Millisecond)
	}
}
