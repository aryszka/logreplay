package logreplay

import "time"

type feedRequest struct {
	position int
	response requestChannel
}

type player struct {
	options            Options
	requestFeed        chan feedRequest
	results            errorChannel
	feed               requestChannel
	position           int
	client             *client
	maxRequestDuration time.Duration
	throttleLag        time.Duration
}

func newPlayer(o Options, requestFeed chan feedRequest, results errorChannel) *player {
	var maxReqDur time.Duration
	if o.Throttle > 0 {
		maxReqDur = time.Second / time.Duration(o.Throttle)
	}

	return &player{
		options:            o,
		requestFeed:        requestFeed,
		results:            results,
		feed:               make(requestChannel),
		client:             newClient(o),
		maxRequestDuration: maxReqDur,
	}
}

func (p *player) throttle(f func() error) error {
	start := time.Now()
	err := f()
	if p.maxRequestDuration <= 0 {
		return err
	}

	duration := time.Now().Sub(start)

	throttle := p.maxRequestDuration - duration
	negativeLag := p.throttleLag < 0
	p.throttleLag += throttle
	if negativeLag && throttle < 0 {
		p.throttleLag = 0
	}

	if p.throttleLag > 0 {
		time.Sleep(p.throttleLag)
		p.throttleLag = 0
	}

	return err
}

func (p *player) run() {
	for {
		select {
		case p.requestFeed <- feedRequest{
			position: p.position,
			response: p.feed,
		}:
		case r, open := <-p.feed:
			if !open {
				return
			}

			if r == nil {
				p.position = 0
				continue
			}

			p.position++

			p.results <- p.throttle(func() error {
				return p.client.do(r)
			})
		}
	}
}
