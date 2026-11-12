package main

import (
	"github.com/miekg/dns"
	"log"
	"strings"
	"sync"
)

func serveDNS(conf config, box *inbox) {
	var mu sync.Mutex
	records := map[string]map[string]bool{}
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		out := new(dns.Msg)
		out.SetReply(r)
		out.Authoritative = true
		box.mu.Lock()
		fail := box.FailDNS
		box.mu.Unlock()
		if r.Opcode == dns.OpcodeUpdate {
			if fail {
				out.Rcode = dns.RcodeServerFailure
			} else if r.IsTsig() == nil || w.TsigStatus() != nil || r.IsTsig().Hdr.Name != "fixture-key." {
				out.Rcode = dns.RcodeRefused
			} else {
				mu.Lock()
				for _, rr := range r.Ns {
					txt, ok := rr.(*dns.TXT)
					if !ok || !strings.HasPrefix(txt.Hdr.Name, "_acme-challenge.") {
						out.Rcode = dns.RcodeRefused
						continue
					}
					value := strings.Join(txt.Txt, "")
					if records[txt.Hdr.Name] == nil {
						records[txt.Hdr.Name] = map[string]bool{}
					}
					if txt.Hdr.Class == dns.ClassNONE {
						delete(records[txt.Hdr.Name], value)
					} else if txt.Hdr.Class == dns.ClassINET {
						records[txt.Hdr.Name][value] = true
					}
				}
				mu.Unlock()
			}
			if r.IsTsig() != nil {
				tsig := r.IsTsig()
				out.SetTsig(tsig.Hdr.Name, tsig.Algorithm, tsig.Fudge, int64(tsig.TimeSigned))
			}
			_ = w.WriteMsg(out)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, q := range r.Question {
			if q.Qtype == dns.TypeTXT {
				for value := range records[q.Name] {
					out.Answer = append(out.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0}, Txt: []string{value}})
				}
			}
		}
		_ = w.WriteMsg(out)
	})
	for _, network := range []string{"udp", "tcp"} {
		server := &dns.Server{Addr: ":8053", Net: network, Handler: handler, TsigSecret: map[string]string{"fixture-key.": conf.DNSSecret}, MsgAcceptFunc: func(h dns.Header) dns.MsgAcceptAction {
			if h.Bits&0x8000 == 0 && (h.Bits>>11)&15 == dns.OpcodeUpdate && h.Qdcount == 1 && h.Nscount <= 20 && h.Arcount <= 2 {
				return dns.MsgAccept
			}
			return dns.DefaultMsgAcceptFunc(h)
		}}
		go func() {
			if e := server.ListenAndServe(); e != nil {
				log.Fatal(e)
			}
		}()
	}
}
