package logreplay

type feedRequest struct {
	position int
	response requestChannel
}

type player struct {
	options     Options
	requestFeed chan feedRequest
	results     errorChannel
	feed        requestChannel
	position    int
	client      *client
}

func newPlayer(o Options, requestFeed chan feedRequest, results errorChannel) *player {
	return &player{
		options:     o,
		requestFeed: requestFeed,
		results:     results,
		feed:        make(requestChannel),
		client:      newClient(o),
	}
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
			err := p.client.do(r)
			p.results <- err
		}
	}
}
