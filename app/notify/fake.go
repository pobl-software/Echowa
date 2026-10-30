package notify

import (
	"context"
	"sync"

	"goravel/app/models"
)

// Fake records what it's asked to send instead of sending it. Tests
// install its Open as notify.Open.
type Fake struct {
	mu   sync.Mutex
	sent []Sent
	// Err, when set, fails every send, or only the first Fails of them
	// when Fails is above 0.
	Err   error
	Fails int
	tries int
}

// Sent is one message the Fake was asked to send, and where to.
type Sent struct {
	Provider models.NotificationProvider
	Message  Message
}

func (f *Fake) Open(p *models.NotificationProvider) (Sender, error) {
	return &fakeSender{fake: f, provider: *p}, nil
}

// Sent lists the messages sent so far, oldest first.
func (f *Fake) Sent() []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]Sent(nil), f.sent...)
}

type fakeSender struct {
	fake     *Fake
	provider models.NotificationProvider
}

func (s *fakeSender) Send(_ context.Context, m Message) error {
	s.fake.mu.Lock()
	defer s.fake.mu.Unlock()
	s.fake.tries++
	if s.fake.Err != nil && (s.fake.Fails == 0 || s.fake.tries <= s.fake.Fails) {
		return s.fake.Err
	}
	s.fake.sent = append(s.fake.sent, Sent{Provider: s.provider, Message: m})

	return nil
}
