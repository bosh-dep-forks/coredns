package cache

import (
	"time"

	"github.com/coredns/coredns/middleware"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"
	"golang.org/x/net/context"
)

// ServeDNS implements the middleware.Handler interface.
func (c *Cache) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}

	qname := state.Name()
	qtype := state.QType()
	zone := middleware.Zones(c.Zones).Matches(qname)
	if zone == "" {
		return c.Next.ServeDNS(ctx, w, r)
	}

	do := state.Do() // TODO(): might need more from OPT record? Like the actual bufsize?

	now := time.Now().UTC()

	i, ttl := c.get(now, qname, qtype, do)
	if i != nil && ttl > 0 {
		resp := i.toMsg(r)

		state.SizeAndDo(resp)
		resp, _ = state.Scrub(resp)
		w.WriteMsg(resp)

		i.Freq.Update(c.duration, now)

		pct := 100
		if i.origTTL != 0 { // you'll never know
			pct = int(float64(ttl) / float64(i.origTTL) * 100)
		}

		if c.prefetch > 0 && i.Freq.Hits() > c.prefetch && pct < c.percentage {
			// When prefetching we loose the item i, and with it the frequency
			// that we've gathered sofar. See we copy the frequencies info back
			// into the new item that was stored in the cache.
			prr := &ResponseWriter{ResponseWriter: w, Cache: c, prefetch: true}
			middleware.NextOrFailure(c.Name(), c.Next, ctx, prr, r)

			if i1, _ := c.get(now, qname, qtype, do); i1 != nil {
				i1.Freq.Reset(now, i.Freq.Hits())
			}
		}

		return dns.RcodeSuccess, nil
	}

	crr := &ResponseWriter{ResponseWriter: w, Cache: c}
	return middleware.NextOrFailure(c.Name(), c.Next, ctx, crr, r)
}

// Name implements the Handler interface.
func (c *Cache) Name() string { return "cache" }

func (c *Cache) get(now time.Time, qname string, qtype uint16, do bool) (*item, int) {
	k := hash(qname, qtype, do)

	if i, ok := c.ncache.Get(k); ok {
		return i.(*item), i.(*item).ttl(now)
	}

	if i, ok := c.pcache.Get(k); ok {
		return i.(*item), i.(*item).ttl(now)
	}
	return nil, 0
}
