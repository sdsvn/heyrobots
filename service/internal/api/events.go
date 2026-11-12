package api

import (
	"encoding/json"
	"sync"
)

// Event is one live update sent to apps over server-sent events.
type Event struct {
	Type string
	Data any
	// About is the device the event concerns, and Audience the app identity
	// it concerns. Delegate devices receive only events about themselves or
	// their own identity; owner devices receive everything.
	About    string
	Audience string
}

type subscriber struct {
	ch       chan Event
	deviceID string
	identity string
	owner    bool
}

// Broker fans events out to connected apps.
type Broker struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

// NewBroker returns an empty Broker.
func NewBroker() *Broker { return &Broker{subs: map[*subscriber]struct{}{}} }

// Subscribe registers a device's stream. The returned channel closes when
// cancel is called or when the stream falls too far behind; the app then
// reconnects and reloads.
func (b *Broker) Subscribe(deviceID, identity string, owner bool) (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, 32), deviceID: deviceID, identity: identity, owner: owner}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() { b.remove(s) }
}

func (b *Broker) remove(s *subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.ch)
	}
}

// Publish sends an event to every stream allowed to see it.
func (b *Broker) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if !s.owner && s.deviceID != e.About && (e.Audience == "" || s.identity != e.Audience) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			// Too far behind: drop the stream rather than block or lose events silently.
			delete(b.subs, s)
			close(s.ch)
		}
	}
}

func encodeEvent(e Event) ([]byte, error) {
	data, err := json.Marshal(e.Data)
	if err != nil {
		return nil, err
	}
	out := append([]byte("event: "+e.Type+"\ndata: "), data...)
	return append(out, '\n', '\n'), nil
}
