package servicebus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	minReconnectDelay = time.Second
	maxReconnectDelay = 30 * time.Second
	// DefaultAnnounceInterval is how often a Bus announces itself unless
	// Options.AnnounceInterval says otherwise.
	DefaultAnnounceInterval = time.Minute
	// tokenRequestTimeout bounds the wait for a TokenReply.
	tokenRequestTimeout = 10 * time.Second
	// directReplyTo is RabbitMQ's pseudo-queue for RPC replies.
	directReplyTo = "amq.rabbitmq.reply-to"
	prefetch      = 4
)

// ErrNotConnected is returned while the bus has no broker connection.
var ErrNotConnected = errors.New("not connected to the message broker")

// ErrNoReply is returned when no instance answered a token request in time.
var ErrNoReply = errors.New("no answer to the token request")

// IssueFunc issues an API token for req. The token is sent sealed to the
// requester; expiresAt is zero for a token that doesn't expire.
type IssueFunc func(ctx context.Context, req TokenRequest) (token string, expiresAt time.Time, err error)

// WatchFunc receives the verified announcements of a watched service. It
// runs on the bus's goroutine, so it must return quickly and must not call
// RequestToken itself.
type WatchFunc func(a Announcement)

// Options configures a Bus.
type Options struct {
	// URL is amqp://user:pass@host:5672/vhost or amqps://… for TLS.
	URL string
	// Secret is shared by every service, at least MinSecretLen bytes.
	Secret []byte
	// Name names the connection in the broker's management UI.
	Name string
	// Self is what this instance announces.
	Self Announcement
	// AnnounceInterval defaults to DefaultAnnounceInterval.
	AnnounceInterval time.Duration
}

// Bus announces this instance, watches others and passes tokens between
// them. Register with ServeTokens and Watch before calling Run.
type Bus struct {
	opts  Options
	codec *codec

	issue    IssueFunc
	watchers map[string][]WatchFunc

	mu sync.Mutex
	// pub publishes and receives direct replies, nil while disconnected.
	pub     *amqp.Channel
	pending map[string]chan []byte
}

// New checks opts and creates a Bus; Run connects it.
func New(opts Options) (*Bus, error) {
	c, err := newCodec(opts.Secret)
	if err != nil {
		return nil, err
	}

	if opts.Self.Service == "" || opts.Self.InstanceID == "" {
		return nil, errors.New("servicebus: Self.Service and Self.InstanceID are required")
	}

	if strings.ContainsAny(opts.Self.Service, ".*#") {
		return nil, fmt.Errorf("servicebus: service name %q must not contain '.', '*' or '#'", opts.Self.Service)
	}

	if opts.AnnounceInterval <= 0 {
		opts.AnnounceInterval = DefaultAnnounceInterval
	}

	return &Bus{opts: opts, codec: c, watchers: map[string][]WatchFunc{}, pending: map[string]chan []byte{}}, nil
}

// ServeTokens has this instance answer the token requests sent to its
// service with issue. Self.IssuesTokens is set to match.
func (b *Bus) ServeTokens(issue IssueFunc) {
	b.issue = issue
	b.opts.Self.IssuesTokens = true
}

// Watch hands every verified announcement of service to fn.
func (b *Bus) Watch(service string, fn WatchFunc) {
	b.watchers[service] = append(b.watchers[service], fn)
}

// Connected reports whether the bus is connected to the broker.
func (b *Bus) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.pub != nil
}

// Run connects to the broker and serves until ctx is done, reconnecting
// with backoff when the connection is lost. It is meant to run in its own
// goroutine.
func (b *Bus) Run(ctx context.Context) {
	delay := minReconnectDelay

	for ctx.Err() == nil {
		start := time.Now()

		err := b.session(ctx)
		if ctx.Err() != nil {
			return
		}

		if time.Since(start) > maxReconnectDelay {
			delay = minReconnectDelay // it was up a while: retry soon
		}

		slog.WarnContext(ctx, "lost the service discovery broker connection", "error", err, "retry_in", delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		delay = min(delay*2, maxReconnectDelay)
	}
}

// streams are the deliveries of one session. tokens is nil unless this
// instance serves tokens.
type streams struct {
	own, tokens, replies <-chan amqp.Delivery
}

// session runs one connection until it fails or ctx is done.
func (b *Bus) session(ctx context.Context) error {
	cfg := amqp.Config{Properties: amqp.NewConnectionProperties()}
	cfg.Properties.SetClientConnectionName(b.opts.Name)

	conn, err := amqp.DialConfig(b.opts.URL, cfg)
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}

	defer func() { _ = conn.Close() }()

	s, err := b.setup(ctx, conn)
	if err != nil {
		return err
	}
	defer b.setPublisher(nil)

	slog.InfoContext(ctx, "connected to service discovery", "exchange", Exchange, "service", b.opts.Self.Service)

	b.announce(ctx)

	for service := range b.watchers {
		b.publish(ctx, TypeDiscover+"."+service, TypeDiscover, Discover{Client: b.opts.Self.Service})
	}

	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	ticker := time.NewTicker(b.opts.AnnounceInterval)

	defer ticker.Stop()

	return b.loop(ctx, s, closed, ticker.C)
}

func (b *Bus) loop(ctx context.Context, s streams, closed <-chan *amqp.Error, tick <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-closed:
			return fmt.Errorf("connection closed: %w", err)
		case <-tick:
			b.announce(ctx)
		case d, ok := <-s.own:
			if !ok {
				return errors.New("consumer cancelled")
			}

			b.handleOwn(ctx, d)
		case d, ok := <-s.tokens:
			if !ok {
				return errors.New("token consumer cancelled")
			}

			if err := b.handleTokenRequest(ctx, d); err != nil {
				return err
			}
		case d, ok := <-s.replies:
			if !ok {
				return errors.New("reply consumer cancelled")
			}

			b.handleReply(d)
		}
	}
}

// setup declares the topology and starts consuming.
func (b *Bus) setup(ctx context.Context, conn *amqp.Connection) (streams, error) {
	ch, err := conn.Channel()
	if err != nil {
		return streams{}, fmt.Errorf("opening channel: %w", err)
	}

	if err := ch.ExchangeDeclare(Exchange, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		return streams{}, fmt.Errorf("declaring exchange %q: %w", Exchange, err)
	}

	var s streams

	if s.own, err = b.consumeOwn(ctx, ch); err != nil {
		return streams{}, err
	}

	if b.issue != nil {
		if s.tokens, err = b.consumeTokens(ctx, conn); err != nil {
			return streams{}, err
		}
	}

	pub, err := conn.Channel()
	if err != nil {
		return streams{}, fmt.Errorf("opening publisher channel: %w", err)
	}

	// Direct reply-to: consume on the channel that publishes the requests.
	if s.replies, err = pub.ConsumeWithContext(ctx, directReplyTo, "", true, false, false, false, nil); err != nil {
		return streams{}, fmt.Errorf("consuming replies: %w", err)
	}

	b.setPublisher(pub)

	return s, nil
}

// consumeOwn binds this instance's private queue to the discover requests
// of its service and the announcements of the watched ones.
func (b *Bus) consumeOwn(ctx context.Context, ch *amqp.Channel) (<-chan amqp.Delivery, error) {
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		return nil, fmt.Errorf("declaring instance queue: %w", err)
	}

	keys := []string{TypeDiscover + "." + b.opts.Self.Service}
	for service := range b.watchers {
		keys = append(keys, TypeAnnounce+"."+service)
	}

	for _, key := range keys {
		if err := ch.QueueBind(q.Name, key, Exchange, false, nil); err != nil {
			return nil, fmt.Errorf("binding %q: %w", key, err)
		}
	}

	deliveries, err := ch.ConsumeWithContext(ctx, q.Name, "", true, true, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("consuming instance queue: %w", err)
	}

	return deliveries, nil
}

// consumeTokens consumes the token requests of this service from a durable
// queue its instances share, so each request is answered once.
func (b *Bus) consumeTokens(ctx context.Context, conn *amqp.Connection) (<-chan amqp.Delivery, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("opening token channel: %w", err)
	}

	queue := Exchange + ".token." + b.opts.Self.Service
	key := "token." + b.opts.Self.Service

	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return nil, fmt.Errorf("declaring queue %q: %w", queue, err)
	}

	if err := ch.QueueBind(queue, key, Exchange, false, nil); err != nil {
		return nil, fmt.Errorf("binding queue %q: %w", queue, err)
	}

	if err := ch.Qos(prefetch, 0, false); err != nil {
		return nil, fmt.Errorf("setting prefetch: %w", err)
	}

	deliveries, err := ch.ConsumeWithContext(ctx, queue, b.opts.Name, false, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("consuming %q: %w", queue, err)
	}

	return deliveries, nil
}

func (b *Bus) setPublisher(pub *amqp.Channel) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.pub = pub
}

func (b *Bus) announce(ctx context.Context) {
	b.publish(ctx, TypeAnnounce+"."+b.opts.Self.Service, TypeAnnounce, b.opts.Self)
}

// publish sends a best-effort, transient message; a failure is logged; the
// next announcement or a reconnect makes up for it.
func (b *Bus) publish(ctx context.Context, key, typ string, body any) {
	msg, _, err := b.codec.seal(typ, body)
	if err == nil {
		err = b.send(ctx, Exchange, key, amqp.Publishing{Body: msg})
	}

	if err != nil {
		slog.WarnContext(ctx, "failed to publish a service message", "routing_key", key, "error", err)
	}
}

func (b *Bus) send(ctx context.Context, exchange, key string, p amqp.Publishing) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.pub == nil {
		return ErrNotConnected
	}

	p.ContentType = "application/json"
	p.AppId = b.opts.Self.Service
	p.Timestamp = time.Now().UTC()

	if err := b.pub.PublishWithContext(ctx, exchange, key, false, false, p); err != nil {
		return fmt.Errorf("publishing to %q: %w", key, err)
	}

	return nil
}

// handleOwn answers a discover request or hands an announcement to its
// watchers. Invalid messages are logged and dropped.
func (b *Bus) handleOwn(ctx context.Context, d amqp.Delivery) {
	kind, service, _ := strings.Cut(d.RoutingKey, ".")

	switch kind {
	case TypeDiscover:
		var req Discover
		if _, err := b.codec.open(d.Body, TypeDiscover, &req); err != nil {
			slog.WarnContext(ctx, "dropped a service discover request", "error", err)
			return
		}

		b.announce(ctx)
	case TypeAnnounce:
		var a Announcement
		if _, err := b.codec.open(d.Body, TypeAnnounce, &a); err != nil {
			slog.WarnContext(ctx, "dropped a service announcement", "routing_key", d.RoutingKey, "error", err)
			return
		}

		if a.Service != service || a.InstanceID == "" {
			slog.WarnContext(ctx, "dropped a service announcement", "routing_key", d.RoutingKey, "service", a.Service)
			return
		}

		for _, fn := range b.watchers[service] {
			fn(a)
		}
	}
}

// handleTokenRequest answers one token request on its reply-to. Only a
// failed acknowledgement is returned: the channel is gone then.
func (b *Bus) handleTokenRequest(ctx context.Context, d amqp.Delivery) error {
	reply, ok := b.answerTokenRequest(ctx, d)
	if ok {
		msg, _, err := b.codec.seal(TypeTokenReply, reply)
		if err == nil {
			err = b.send(ctx, "", d.ReplyTo, amqp.Publishing{CorrelationId: d.CorrelationId, Body: msg})
		}

		if err != nil {
			slog.WarnContext(ctx, "failed to answer a token request", "error", err)
		}
	}

	if err := d.Ack(false); err != nil {
		return fmt.Errorf("acknowledging a token request: %w", err)
	}

	return nil
}

// answerTokenRequest returns the reply to d, or false for a request to drop
// unanswered: one that can't be verified or has nowhere to go.
func (b *Bus) answerTokenRequest(ctx context.Context, d amqp.Delivery) (TokenReply, bool) {
	var req TokenRequest

	nonce, err := b.codec.open(d.Body, TypeTokenRequest, &req)
	if err == nil && !b.codec.fresh(nonce) {
		err = fmt.Errorf("%w: replayed request", ErrInvalid)
	}

	if err == nil && (d.ReplyTo == "" || d.CorrelationId == "") {
		err = fmt.Errorf("%w: no reply_to or correlation_id", ErrInvalid)
	}

	if err != nil {
		slog.WarnContext(ctx, "dropped a token request", "error", err)
		return TokenReply{}, false
	}

	reply := TokenReply{RequestNonce: nonce}

	token, expiresAt, err := b.issue(ctx, req)
	if err != nil {
		slog.WarnContext(ctx, "refused a token request", "client", req.Client, "client_instance", req.ClientInstance, "error", err)
		reply.Error = err.Error()

		return reply, true
	}

	if reply.SealedToken, err = b.codec.sealToken(nonce, token); err != nil {
		reply.Error = "sealing the token failed"
		return reply, true
	}

	reply.ExpiresAt = expiresAt

	return reply, true
}

func (b *Bus) handleReply(d amqp.Delivery) {
	b.mu.Lock()
	ch, ok := b.pending[d.CorrelationId]
	b.mu.Unlock()

	if ok {
		select {
		case ch <- d.Body:
		default:
		}
	}
}

// RequestToken asks an instance of service for an API token for this
// instance. It fails with ErrNotConnected while disconnected and with
// ErrNoReply when nobody answers within ten seconds.
func (b *Bus) RequestToken(ctx context.Context, service string) (string, time.Time, error) {
	msg, nonce, err := b.codec.seal(TypeTokenRequest, TokenRequest{Client: b.opts.Self.Service, ClientInstance: b.opts.Self.InstanceID})
	if err != nil {
		return "", time.Time{}, err
	}

	replies := make(chan []byte, 1)

	b.mu.Lock()
	b.pending[nonce] = replies
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pending, nonce)
		b.mu.Unlock()
	}()

	err = b.send(ctx, Exchange, "token."+service, amqp.Publishing{CorrelationId: nonce, ReplyTo: directReplyTo, Body: msg})
	if err != nil {
		return "", time.Time{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, tokenRequestTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return "", time.Time{}, fmt.Errorf("%w from %s: %w", ErrNoReply, service, ctx.Err())
	case body := <-replies:
		return b.readReply(nonce, body)
	}
}

func (b *Bus) readReply(nonce string, body []byte) (string, time.Time, error) {
	var reply TokenReply
	if _, err := b.codec.open(body, TypeTokenReply, &reply); err != nil {
		return "", time.Time{}, err
	}

	if reply.RequestNonce != nonce {
		return "", time.Time{}, fmt.Errorf("%w: reply to another request", ErrInvalid)
	}

	if reply.Error != "" {
		return "", time.Time{}, fmt.Errorf("token request refused: %s", reply.Error)
	}

	token, err := b.codec.openToken(nonce, reply.SealedToken)
	if err != nil {
		return "", time.Time{}, err
	}

	return token, reply.ExpiresAt, nil
}
