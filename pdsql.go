// Package pdsql implements a plugin that query powerdns database to resolve the coredns query
package pdsql

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/wenerme/coredns-pdsql/pdnsmodel"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"golang.org/x/net/context"
	"gorm.io/gorm"
)

const Name = "pdsql"

type PowerDNSGenericSQLBackend struct {
	*gorm.DB
	Debug bool
	Next  plugin.Handler
}

func (pdb PowerDNSGenericSQLBackend) Name() string { return Name }
func (pdb PowerDNSGenericSQLBackend) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	a := new(dns.Msg)
	a.SetReply(r)
	a.Compress = true
	a.Authoritative = true

	var records []*pdnsmodel.Record
	query := pdnsmodel.Record{Name: state.QName(), Type: state.Type(), Disabled: false}
	if query.Name != "." {
		// remove last dot
		query.Name = query.Name[:len(query.Name)-1]
	}

	switch state.QType() {
	case dns.TypeANY:
		query.Type = ""
	}

	if err := pdb.Where(query).Find(&records).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			query.Type = "SOA"
			if pdb.Where(query).Find(&records).Error == nil {
				rr := new(dns.SOA)
				rr.Hdr = dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeSOA, Class: state.QClass()}
				if ParseSOA(rr, records[0].Content) {
					a.Extra = append(a.Extra, rr)
				}
			}
		} else {
			return dns.RcodeServerFailure, err
		}
	} else {
		if len(records) == 0 {
			records, err = pdb.ResolveCNAMEs(state.QName())
			if err != nil {
				return dns.RcodeServerFailure, err
			}
		}

		if len(records) == 0 {
			records, err = pdb.SearchWildcard(state.QName(), state.QType())
			if err != nil {
				return dns.RcodeServerFailure, err
			}
		}

		for _, v := range records {
			typ := dns.StringToType[v.Type]
			hrd := dns.RR_Header{Name: v.Name, Rrtype: typ, Class: state.QClass(), Ttl: v.Ttl}
			if !strings.HasSuffix(hrd.Name, ".") {
				hrd.Name += "."
			}
			rr := dns.TypeToRR[typ]()

			// todo support more type
			// this is enough for most query
			switch rr := rr.(type) {
			case *dns.SOA:
				rr.Hdr = hrd
				if !ParseSOA(rr, v.Content) {
					rr = nil
				}
			case *dns.A:
				rr.Hdr = hrd
				rr.A = net.ParseIP(v.Content)
			case *dns.AAAA:
				rr.Hdr = hrd
				rr.AAAA = net.ParseIP(v.Content)
			case *dns.TXT:
				rr.Hdr = hrd
				rr.Txt = []string{v.Content}
			case *dns.NS:
				rr.Hdr = hrd
				rr.Ns = v.Content
			case *dns.PTR:
				rr.Hdr = hrd
				// pdns don't need the dot but when we answer, we need it
				if strings.HasSuffix(v.Content, ".") {
					rr.Ptr = v.Content
				} else {
					rr.Ptr = v.Content + "."
				}
			case *dns.CNAME:
				rr.Hdr = hrd
				rr.Target = v.Content

			case *dns.MX:
				rr.Hdr = hrd
				parts := strings.Split(v.Content, " ")
				if len(parts) == 2 {
					preference, host := parts[0], parts[1]
					if pref, err := strconv.Atoi(preference); err == nil {
						rr.Preference = uint16(pref)
					} else {
						return dns.RcodeServerFailure, fmt.Errorf("invalid MX preference: %s", preference)
					}
					rr.Mx = host
				} else {
					return dns.RcodeServerFailure, fmt.Errorf("malformed MX record content: %s", v.Content)
				}

			case *dns.SRV:
				rr.Hdr = hrd
				parts := strings.Split(v.Content, " ")
				if len(parts) != 4 {
					return dns.RcodeServerFailure, fmt.Errorf("malformed SRV record content: %s - parts=%d", v.Content, len(parts))
				}
				if priority, err := strconv.Atoi(parts[0]); err == nil {
					rr.Priority = uint16(priority)
				} else {
					return dns.RcodeServerFailure, fmt.Errorf("invalid SRV priority: %s", parts[0])
				}
				if weight, err := strconv.Atoi(parts[1]); err == nil {
					rr.Weight = uint16(weight)
				} else {
					return dns.RcodeServerFailure, fmt.Errorf("invalid SRV weight: %s", parts[1])
				}
				if port, err := strconv.Atoi(parts[2]); err == nil {
					rr.Port = uint16(port)
				} else {
					return dns.RcodeServerFailure, fmt.Errorf("invalid SRV port: %s", parts[2])
				}
				rr.Target = parts[3]
			default:
				// drop unsupported
			}

			if rr == nil {
				// invalid record
			} else {
				a.Answer = append(a.Answer, rr)
			}
		}
	}
	if len(a.Answer) == 0 {
		return plugin.NextOrFailure(pdb.Name(), pdb.Next, ctx, w, r)
	}

	return 0, w.WriteMsg(a)
}

func (pdb *PowerDNSGenericSQLBackend) ResolveCNAMEs(cname string) ([]*pdnsmodel.Record, error) {
	resolveTypes := []string{"CNAME", "A", "AAA"}
	resolveCNAME := true

	var cnameRecords []*pdnsmodel.Record
	var err error

	if strings.HasSuffix(cname, ".") {
		// remove last dot
		cname = strings.TrimSuffix(cname, ".")
	}

	for resolveCNAME {
		var cnameRecord = pdnsmodel.Record{Name: cname}

		if err := pdb.Where(&cnameRecord).Where(map[string]interface{}{"type": &resolveTypes}).Find(&cnameRecord).Error; err != nil {
			return nil, err
		}

		if cnameRecord.ID != 0 && cnameRecord.Content != "" {
			// Save successful records
			cnameRecords = append(cnameRecords, &cnameRecord)

			// Resolve resursively
			if cnameRecord.Type == "CNAME" {
				// Update Search
				cname = cnameRecord.Content

				if strings.HasSuffix(cname, ".") {
					// remove last dot
					cname = strings.TrimSuffix(cname, ".")
				}
			} else {
				resolveCNAME = false
			}
		} else {
			resolveCNAME = false
		}
	}

	return cnameRecords, err
}

func (pdb PowerDNSGenericSQLBackend) SearchWildcard(qname string, qtype uint16) (redords []*pdnsmodel.Record, err error) {
	// find domain, then find matched sub domain
	name := qname
	qnameNoDot := qname[:len(qname)-1]
	typ := dns.TypeToString[qtype]
	name = qnameNoDot
NEXT_ZONE:
	if i := strings.IndexRune(name, '.'); i > 0 {
		name = name[i+1:]
	} else {
		return
	}
	var domain pdnsmodel.Domain

	if err := pdb.Limit(1).Find(&domain, "name = ?", name).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			goto NEXT_ZONE
		}
		return nil, err
	}

	if err := pdb.Find(&redords, "domain_id = ? and ( ? = 'ANY' or type = ? ) and name like '%*%'", domain.ID, typ, typ).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}

	// filter
	var matched []*pdnsmodel.Record
	for _, v := range redords {
		if WildcardMatch(qnameNoDot, v.Name) {
			matched = append(matched, v)
		}
	}
	redords = matched
	return
}

func ParseSOA(rr *dns.SOA, line string) bool {
	splites := strings.Split(line, " ")
	if len(splites) < 7 {
		return false
	}
	rr.Ns = splites[0]
	rr.Mbox = splites[1]
	if i, err := strconv.Atoi(splites[2]); err != nil {
		return false
	} else {
		rr.Serial = uint32(i)
	}
	if i, err := strconv.Atoi(splites[3]); err != nil {
		return false
	} else {
		rr.Refresh = uint32(i)
	}
	if i, err := strconv.Atoi(splites[4]); err != nil {
		return false
	} else {
		rr.Retry = uint32(i)
	}
	if i, err := strconv.Atoi(splites[5]); err != nil {
		return false
	} else {
		rr.Expire = uint32(i)
	}
	if i, err := strconv.Atoi(splites[6]); err != nil {
		return false
	} else {
		rr.Minttl = uint32(i)
	}
	return true
}

// Dummy wildcard match
func WildcardMatch(s1, s2 string) bool {
	if s1 == "." || s2 == "." {
		return true
	}

	l1 := dns.SplitDomainName(s1)
	l2 := dns.SplitDomainName(s2)

	if len(l1) != len(l2) {
		return false
	}

	for i := range l1 {
		if !equal(l1[i], l2[i]) {
			return false
		}
	}

	return true
}

func equal(a, b string) bool {
	if b == "*" || a == "*" {
		return true
	}
	// might be lifted into API function.
	la := len(a)
	lb := len(b)
	if la != lb {
		return false
	}

	for i := la - 1; i >= 0; i-- {
		ai := a[i]
		bi := b[i]
		if ai >= 'A' && ai <= 'Z' {
			ai |= 'a' - 'A'
		}
		if bi >= 'A' && bi <= 'Z' {
			bi |= 'a' - 'A'
		}
		if ai != bi {
			return false
		}
	}
	return true
}
