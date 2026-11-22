// Package servicebus lets the TMD services announce themselves over RabbitMQ
// and hand each other API tokens, so nobody has to copy URLs and tokens
// between their settings.
//
// Every message goes through one durable topic exchange (Exchange):
//
//	announce.<service>  a service's Announcement: at start, every
//	                    AnnounceInterval and when asked by a discover
//	discover.<service>  asks the instances of <service> to announce now
//	token.<service>     a TokenRequest, answered on the AMQP reply-to with
//	                    a TokenReply holding a fresh API token of <service>
//
// Broker access alone is not trusted: every message is signed with the
// secret the services share (an HMAC over type, time, nonce and body), and
// a receiver drops messages it can't verify, of an unknown schema_version
// or older than MaxSkew. The token in a reply is sealed (AES-GCM, with a key
// derived from the secret and the request's nonce), so it never crosses the
// broker readable.
//
// This package is copied verbatim into each service that speaks the
// protocol (the tile server, address-serv); keep the copies identical.
package servicebus

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// SchemaVersion is the version of the envelope this package writes and reads.
const SchemaVersion = 1

// Exchange is the topic exchange every service message goes through.
const Exchange = "tmd.services"

// MinSecretLen is the shortest shared secret accepted, in bytes.
const MinSecretLen = 32

// MaxSkew is how far a message's time may be from the receiver's clock.
// Older messages are dropped, so a recorded one can't be replayed later.
const MaxSkew = 5 * time.Minute

// Message types.
const (
	TypeAnnounce     = "announce"
	TypeDiscover     = "discover"
	TypeTokenRequest = "token_request"
	TypeTokenReply   = "token_reply"
)

// ErrInvalid marks a message that failed verification.
var ErrInvalid = errors.New("invalid service message")

// Announcement describes a running service instance.
type Announcement struct {
	// Service is the kind of service, e.g. "address" or "tiles".
	Service string `json:"service"`
	// InstanceID tells the instances of a service apart.
	InstanceID string `json:"instance_id"`
	// Name is a human-readable name of the instance.
	Name string `json:"name,omitempty"`
	// URL is where the other services reach it.
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`
	// IssuesTokens is true when it answers token requests.
	IssuesTokens bool `json:"issues_tokens"`
}

// Discover asks the instances of a service to announce themselves.
type Discover struct {
	Client string `json:"client"`
}

// TokenRequest asks a service for an API token.
type TokenRequest struct {
	// Client is the requesting kind of service, e.g. "tiles".
	Client string `json:"client"`
	// ClientInstance is the requesting instance; a new request replaces the
	// token issued to the same instance before.
	ClientInstance string `json:"client_instance"`
}

// TokenReply answers a TokenRequest.
type TokenReply struct {
	// RequestNonce is the nonce of the request answered.
	RequestNonce string `json:"request_nonce"`
	// SealedToken is the token, sealed with sealToken.
	SealedToken string    `json:"sealed_token,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitzero"`
	// Error is set instead of a token when the request was refused.
	Error string `json:"error,omitempty"`
}

// envelope is a message on the wire.
type envelope struct {
	SchemaVersion int             `json:"schema_version"`
	Type          string          `json:"type"`
	TS            time.Time       `json:"ts"`
	Nonce         string          `json:"nonce"`
	Body          json.RawMessage `json:"body"`
	MAC           string          `json:"mac"`
}

// codec signs and verifies messages with the shared secret.
type codec struct {
	secret []byte
	now    func() time.Time

	mu sync.Mutex
	// seen holds the nonces of token requests already accepted, until
	// they're older than MaxSkew.
	seen map[string]time.Time
}

func newCodec(secret []byte) (*codec, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("the service discovery secret must be at least %d bytes", MinSecretLen)
	}

	return &codec{secret: secret, now: time.Now, seen: map[string]time.Time{}}, nil
}

// seal returns body as a signed envelope of the given type, and its nonce.
func (c *codec) seal(typ string, body any) ([]byte, string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("encoding %s: %w", typ, err)
	}

	nonce, err := randomHex(16)
	if err != nil {
		return nil, "", err
	}

	e := envelope{SchemaVersion: SchemaVersion, Type: typ, TS: c.now().UTC(), Nonce: nonce, Body: raw}
	e.MAC = c.mac(e)

	out, err := json.Marshal(e)
	if err != nil {
		return nil, "", fmt.Errorf("encoding %s: %w", typ, err)
	}

	return out, nonce, nil
}

// open verifies msg as an envelope of type typ, decodes its body into out
// and returns its nonce. Every failure wraps ErrInvalid.
func (c *codec) open(msg []byte, typ string, out any) (string, error) {
	var e envelope
	if err := json.Unmarshal(msg, &e); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if e.SchemaVersion != SchemaVersion {
		return "", fmt.Errorf("%w: unsupported schema_version %d", ErrInvalid, e.SchemaVersion)
	}

	if e.Type != typ {
		return "", fmt.Errorf("%w: type %q, want %q", ErrInvalid, e.Type, typ)
	}

	want, err := hex.DecodeString(c.mac(e))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	got, err := hex.DecodeString(e.MAC)
	if err != nil || !hmac.Equal(got, want) {
		return "", fmt.Errorf("%w: bad signature (is the shared secret the same?)", ErrInvalid)
	}

	if skew := c.now().Sub(e.TS); skew > MaxSkew || skew < -MaxSkew {
		return "", fmt.Errorf("%w: sent at %s, too far from now", ErrInvalid, e.TS.Format(time.RFC3339))
	}

	if e.Nonce == "" {
		return "", fmt.Errorf("%w: no nonce", ErrInvalid)
	}

	if err := json.Unmarshal(e.Body, out); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return e.Nonce, nil
}

// mac signs everything in e but the MAC itself. The parts are
// length-prefixed, so no two different envelopes sign the same bytes.
func (c *codec) mac(e envelope) string {
	h := hmac.New(sha256.New, c.secret)

	for _, part := range []string{Exchange, strconv.Itoa(e.SchemaVersion), e.Type, e.TS.UTC().Format(time.RFC3339Nano), e.Nonce, string(e.Body)} {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(part), part)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// fresh records nonce and reports whether it was not seen before.
func (c *codec) fresh(nonce string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	for n, at := range c.seen {
		if now.Sub(at) > 2*MaxSkew {
			delete(c.seen, n)
		}
	}

	if _, ok := c.seen[nonce]; ok {
		return false
	}

	c.seen[nonce] = now

	return true
}

// sealToken encrypts token for the requester of requestNonce.
func (c *codec) sealToken(requestNonce, token string) (string, error) {
	aead, err := c.tokenAEAD(requestNonce)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("sealing token: %w", err)
	}

	sealed := aead.Seal(nonce, nonce, []byte(token), []byte(requestNonce))

	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openToken decrypts what sealToken made for requestNonce.
func (c *codec) openToken(requestNonce, sealed string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	aead, err := c.tokenAEAD(requestNonce)
	if err != nil {
		return "", err
	}

	if len(raw) < aead.NonceSize() {
		return "", fmt.Errorf("%w: sealed token too short", ErrInvalid)
	}

	token, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(requestNonce))
	if err != nil {
		return "", fmt.Errorf("%w: unsealing token: %w", ErrInvalid, err)
	}

	return string(token), nil
}

func (c *codec) tokenAEAD(requestNonce string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, c.secret, nil, "tmd.services token "+requestNonce, 32)
	if err != nil {
		return nil, fmt.Errorf("deriving token key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("token cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("token cipher: %w", err)
	}

	return aead, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}

	return hex.EncodeToString(b), nil
}
