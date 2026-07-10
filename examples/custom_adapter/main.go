package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/loykin/dbstore"
)

// QueueClient stands in for a backend SDK client that dbstore does not provide
// an official adapter package for.
type QueueClient struct {
	Endpoint *url.URL
	messages map[string][]string
}

func (c *QueueClient) Publish(ctx context.Context, topic, message string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		c.messages[topic] = append(c.messages[topic], message)
		return nil
	}
}

func (c *QueueClient) Last(ctx context.Context, topic string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
		messages := c.messages[topic]
		if len(messages) == 0 {
			return "", fmt.Errorf("topic %q has no messages", topic)
		}
		return messages[len(messages)-1], nil
	}
}

type QueueDriver struct{}

func (d QueueDriver) Open(cfg dbstore.SourceConfig) (*QueueClient, error) {
	endpoint, err := url.Parse(cfg.DSN)
	if err != nil {
		return nil, err
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("queue dsn must be an absolute URL")
	}
	if !strings.HasSuffix(endpoint.Path, "/") {
		endpoint.Path += "/"
	}
	return &QueueClient{
		Endpoint: endpoint,
		messages: make(map[string][]string),
	}, nil
}

type eventRepo struct {
	source dbstore.Source[*QueueClient]
	topic  string
}

func newEventRepo(exec *dbstore.Executor[*QueueClient], source, topic string) *eventRepo {
	return &eventRepo{
		source: dbstore.NewSource(source, exec),
		topic:  topic,
	}
}

func (r *eventRepo) Publish(ctx context.Context, message string) error {
	return r.source.Run(ctx, func(ctx context.Context, client *QueueClient) error {
		return client.Publish(ctx, r.topic, message)
	})
}

func (r *eventRepo) Last(ctx context.Context) (string, error) {
	var message string
	err := r.source.Run(ctx, func(ctx context.Context, client *QueueClient) error {
		var err error
		message, err = client.Last(ctx, r.topic)
		return err
	})
	return message, err
}

func main() {
	ctx := context.Background()

	queue := dbstore.NewAdapter[*QueueClient]()
	queue.RegisterDriver("queue", QueueDriver{})
	defer queue.Close()

	if err := queue.Open("events", dbstore.SourceConfig{
		Driver: "queue",
		DSN:    "memory://events",
	}); err != nil {
		log.Fatal(err)
	}

	repo := newEventRepo(queue.Executor(), "events", "user.created")
	if err := repo.Publish(ctx, "Alice"); err != nil {
		log.Fatal(err)
	}

	message, err := repo.Last(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(message)
}
