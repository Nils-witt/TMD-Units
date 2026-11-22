package servicebus

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const testClient = "tiles"

var (
	testSecret  = []byte("0123456789abcdef0123456789abcdef")
	otherSecret = []byte("fedcba9876543210fedcba9876543210")
)

func newTestCodec(t *testing.T, secret []byte, now time.Time) *codec {
	t.Helper()

	c, err := newCodec(secret)
	if err != nil {
		t.Fatal(err)
	}

	c.now = func() time.Time { return now }

	return c
}

func TestNewCodecRejectsShortSecret(t *testing.T) {
	t.Parallel()

	if _, err := newCodec([]byte("short")); err == nil {
		t.Error("accepted a short secret")
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c := newTestCodec(t, testSecret, now)

	want := Announcement{Service: "address", InstanceID: "i1", URL: "http://address:8080", IssuesTokens: true}

	msg, nonce, err := c.seal(TypeAnnounce, want)
	if err != nil {
		t.Fatal(err)
	}

	var got Announcement

	gotNonce, err := c.open(msg, TypeAnnounce, &got)
	if err != nil || got != want || gotNonce != nonce {
		t.Fatalf("open = %+v, %q, %v; want %+v, %q", got, gotNonce, err, want, nonce)
	}
}

func TestOpenRejects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c := newTestCodec(t, testSecret, now)

	msg, _, err := c.seal(TypeAnnounce, Announcement{Service: "address", URL: "http://address:8080"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		msg   []byte
		typ   string
		codec *codec
	}{
		{"not JSON", []byte("nope"), TypeAnnounce, c},
		{"wrong type", msg, TypeDiscover, c},
		{"other secret", msg, TypeAnnounce, newTestCodec(t, otherSecret, now)},
		{"tampered body", bytes.Replace(msg, []byte("address:8080"), []byte("evil.example"), 1), TypeAnnounce, c},
		{"unknown version", bytes.Replace(msg, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1), TypeAnnounce, c},
		{"too old", msg, TypeAnnounce, newTestCodec(t, testSecret, now.Add(MaxSkew+time.Second))},
		{"from the future", msg, TypeAnnounce, newTestCodec(t, testSecret, now.Add(-MaxSkew-time.Second))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var a Announcement
			if _, err := tt.codec.open(tt.msg, tt.typ, &a); !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestMACCoversEveryField(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c := newTestCodec(t, testSecret, now)

	msg, _, err := c.seal(TypeDiscover, Discover{Client: testClient})
	if err != nil {
		t.Fatal(err)
	}

	var e envelope
	if err := json.Unmarshal(msg, &e); err != nil {
		t.Fatal(err)
	}

	mutations := map[string]func(*envelope){
		"type":  func(e *envelope) { e.Type = TypeAnnounce },
		"ts":    func(e *envelope) { e.TS = e.TS.Add(time.Second) },
		"nonce": func(e *envelope) { e.Nonce += "0" },
		"body":  func(e *envelope) { e.Body = json.RawMessage(`{"client":"api"}`) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			changed := e
			mutate(&changed)

			if c.mac(changed) == e.MAC {
				t.Errorf("changing %s keeps the MAC", name)
			}
		})
	}
}

func TestFreshRejectsReplays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c := newTestCodec(t, testSecret, now)

	if !c.fresh("n1") {
		t.Fatal("first use not fresh")
	}

	if c.fresh("n1") {
		t.Error("replay accepted")
	}

	if !c.fresh("n2") {
		t.Error("other nonce not fresh")
	}
}

func TestSealToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c := newTestCodec(t, testSecret, now)

	sealed, err := c.sealToken("nonce-a", "asv_secret")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(sealed, "asv_secret") {
		t.Fatal("token readable in the sealed form")
	}

	if got, err := c.openToken("nonce-a", sealed); err != nil || got != "asv_secret" {
		t.Fatalf("openToken = %q, %v", got, err)
	}

	if _, err := c.openToken("nonce-b", sealed); !errors.Is(err, ErrInvalid) {
		t.Errorf("opened with another request's nonce: %v", err)
	}

	if _, err := newTestCodec(t, otherSecret, now).openToken("nonce-a", sealed); !errors.Is(err, ErrInvalid) {
		t.Errorf("opened with another secret: %v", err)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	t.Parallel()

	self := Announcement{Service: testClient, InstanceID: "i1"}

	tests := []struct {
		name string
		opts Options
		ok   bool
	}{
		{"valid", Options{Secret: testSecret, Self: self}, true},
		{"short secret", Options{Secret: []byte("x"), Self: self}, false},
		{"no service", Options{Secret: testSecret, Self: Announcement{InstanceID: "i1"}}, false},
		{"no instance", Options{Secret: testSecret, Self: Announcement{Service: testClient}}, false},
		{"dotted service", Options{Secret: testSecret, Self: Announcement{Service: "a.b", InstanceID: "i1"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := New(tt.opts); (err == nil) != tt.ok {
				t.Errorf("New err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestRequestTokenWithoutConnection(t *testing.T) {
	t.Parallel()

	b, err := New(Options{Secret: testSecret, Self: Announcement{Service: testClient, InstanceID: "i1"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := b.RequestToken(t.Context(), "address"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestReadReply(t *testing.T) {
	t.Parallel()

	now := time.Now()

	b, err := New(Options{Secret: testSecret, Self: Announcement{Service: testClient, InstanceID: "i1"}})
	if err != nil {
		t.Fatal(err)
	}

	sealed, err := b.codec.sealToken("req", "asv_x")
	if err != nil {
		t.Fatal(err)
	}

	expires := now.Add(time.Hour).UTC().Truncate(time.Second)

	reply := func(r TokenReply) []byte {
		msg, _, err := b.codec.seal(TypeTokenReply, r)
		if err != nil {
			t.Fatal(err)
		}

		return msg
	}

	token, gotExpires, err := b.readReply("req", reply(TokenReply{RequestNonce: "req", SealedToken: sealed, ExpiresAt: expires}))
	if err != nil || token != "asv_x" || !gotExpires.Equal(expires) {
		t.Fatalf("readReply = %q, %v, %v", token, gotExpires, err)
	}

	if _, _, err := b.readReply("other", reply(TokenReply{RequestNonce: "req", SealedToken: sealed})); !errors.Is(err, ErrInvalid) {
		t.Errorf("accepted a reply to another request: %v", err)
	}

	if _, _, err := b.readReply("req", reply(TokenReply{RequestNonce: "req", Error: "not allowed"})); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("refusal err = %v", err)
	}
}
