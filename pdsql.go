// Package pdsql implements a plugin that query powerdns database to resolve the coredns query
package pdsql

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"pdsql/pdnsmodel"

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

	records, err := pdb.ResolveRequest(state.QName(), state.QType())

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			soa, err := pdb.ResolveSOA(state.QName())

			if err != nil {
				return dns.RcodeServerFailure, err
			}

			rr := new(dns.SOA)
			rr.Hdr = dns.RR_Header{Name: state.QName(), Rrtype: dns.TypeSOA, Class: state.QClass()}

			if ParseSOA(rr, soa.Content) {
				a.Extra = append(a.Extra, rr)
			}
		} else {
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
			if strings.HasSuffix(v.Content, ".") {
				rr.Ns = v.Content
			} else {
				rr.Ns = v.Content + "."
			}
		case *dns.PTR:
			rr.Hdr = hrd
			// pdns doesn't need the dot but when we answer, we need it
			if strings.HasSuffix(v.Content, ".") {
				rr.Ptr = v.Content
			} else {
				rr.Ptr = v.Content + "."
			}
		case *dns.CNAME:
			rr.Hdr = hrd
			if strings.HasSuffix(v.Content, ".") {
				rr.Target = v.Content
			} else {
				rr.Target = v.Content + "."
			}

		case *dns.MX:
			rr.Hdr = hrd

			// PowerDNS requires for MX Records the Priority to be set
			if v.Prio != 0 {
				rr.Preference = uint16(v.Prio)
				if strings.HasSuffix(v.Content, ".") {
					rr.Mx = v.Content
				} else {
					rr.Mx = v.Content + "."
				}
			} else {
				parts := strings.Split(v.Content, " ")

				if len(parts) == 2 {
					preference, host := parts[0], parts[1]
					if pref, err := strconv.Atoi(preference); err == nil {
						rr.Preference = uint16(pref)
					} else {
						return dns.RcodeServerFailure, fmt.Errorf("invalid MX preference: %s", preference)
					}
					if strings.HasSuffix(host, ".") {
						rr.Mx = host
					} else {
						rr.Mx = host + "."
					}
				} else {
					return dns.RcodeServerFailure, fmt.Errorf("malformed MX record content: %s", v.Content)
				}
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

	if len(a.Answer) == 0 {
		return plugin.NextOrFailure(pdb.Name(), pdb.Next, ctx, w, r)
	}

	return 0, w.WriteMsg(a)
}

func (pdb *PowerDNSGenericSQLBackend) ResolveRequest(qname string, qtype uint16) ([]*pdnsmodel.Record, error) {
	var resRecords []*pdnsmodel.Record
	var err error

	qname = strings.ToLower(qname)

	if strings.HasSuffix(qname, ".") {
		// remove last dot
		qname = strings.TrimSuffix(qname, ".")
	}

	var queryRecords []pdnsmodel.Record
	query := pdb.Model(&pdnsmodel.Record{}).
		Where("name = ?", qname)

	switch qtype {
	case dns.TypeANY:
		// Do not add any type query
	case dns.TypeCNAME:
		query = query.Where("type = ?", dns.TypeToString[qtype])
	default:
		resolveTypes := []string{"CNAME", dns.TypeToString[qtype]}
		query = query.Where(map[string]interface{}{"type": &resolveTypes})
	}

	query = query.Where("disabled = ?", false)

	if err := query.Find(&queryRecords).Error; err != nil {
		return nil, err
	}

	if len(queryRecords) != 0 {
		for recIndex, queryRec := range queryRecords {
			// Save successful records
			resRecords = append(resRecords, &queryRecords[recIndex])

			// Resolve CNAME entries
			if queryRec.Type == "CNAME" && qtype != dns.TypeANY {
				if cnameRecords, err := pdb.ResolveCNAMEs(queryRec.Content, qtype); err == nil {
					for _, r := range cnameRecords {
						resRecords = append(resRecords, r)
					}
				} else {
					return nil, err
				}
			}
		}
	}

	return resRecords, err
}

func (pdb *PowerDNSGenericSQLBackend) ResolveSOA(qname string) (*pdnsmodel.Record, error) {
	var soaRecord pdnsmodel.Record

	qname = strings.ToLower(qname)

	if strings.HasSuffix(qname, ".") {
		// remove last dot
		qname = strings.TrimSuffix(qname, ".")
	}

	query := pdb.Model(&soaRecord).
		Where("name = ?", qname).
		Where("type = ?", "SOA").
		Where("disabled = ?", false)

	if err := query.Find(&soaRecord).Error; err == nil {
		fmt.Printf("pdsql - ResolveRequest(): soa rec: %#v'\n", soaRecord)

		return &soaRecord, err
	} else {
		return nil, err
	}
}

func (pdb *PowerDNSGenericSQLBackend) ResolveCNAMEs(cname string, qtype uint16) ([]*pdnsmodel.Record, error) {
	var resolveTypes []string
	resolveCNAME := true

	var cnameRecords []*pdnsmodel.Record
	var err error

	cname = strings.ToLower(cname)

	if strings.HasSuffix(cname, ".") {
		// remove last dot
		cname = strings.TrimSuffix(cname, ".")
	}

	switch qtype {
	case dns.TypeANY:
		// Do not add any type query
	case dns.TypeCNAME:
		resolveTypes = []string{dns.TypeToString[qtype]}
	default:
		resolveTypes = []string{"CNAME", dns.TypeToString[qtype]}
	}

	for resolveCNAME {
		var queryRecords []pdnsmodel.Record
		query := pdb.Model(&pdnsmodel.Record{}).
			Where("name = ?", cname)

		if len(resolveTypes) != 0 {
			query = query.Where(map[string]interface{}{"type": &resolveTypes})
		}

		query = query.Where("disabled = ?", false)

		if err := query.Find(&queryRecords).Error; err != nil {
			return nil, err
		}

		if len(queryRecords) != 0 {
			for recIndex, queryRec := range queryRecords {
				// Save successful records
				cnameRecords = append(cnameRecords, &queryRecords[recIndex])

				// Resolve resursively
				if queryRec.Type == "CNAME" {
					// Update Search
					cname = queryRec.Content

					if strings.HasSuffix(cname, ".") {
						// remove last dot
						cname = strings.TrimSuffix(cname, ".")
					}
				} else {
					resolveCNAME = false
				}
			}
		} else {
			resolveCNAME = false
		}
	}

	return cnameRecords, err
}

func (pdb *PowerDNSGenericSQLBackend) SearchWildcard(qname string, qtype uint16) ([]*pdnsmodel.Record, error) {
	// find domain, then find matched sub domain
	searchName := strings.TrimSuffix(strings.ToLower(qname), ".")

	domain, err := pdb.SearchDomain(qname)

	if err != nil {
		return nil, err
	}

	if domain == nil {
		return nil, nil
	}

	var astRecords []pdnsmodel.Record
	query := pdb.Model(&pdnsmodel.Record{}).
		Where("domain_id = ?", (*domain).ID).
		Where("name LIKE ?", "*.%")

	switch qtype {
	case dns.TypeANY:
		// Do not add any type query
	default:
		typeValues := []string{"CNAME", dns.TypeToString[qtype]}
		query = query.Where(map[string]interface{}{"type": &typeValues})
	}

	query = query.Where("disabled = ?", false)

	if err := query.Find(&astRecords).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}

	// filter
	var matched []*pdnsmodel.Record

	for matchIndex, astRec := range astRecords {
		if WildcardMatch(searchName, astRec.Name) {
			astRecords[matchIndex].Name = searchName

			matched = append(matched, &astRecords[matchIndex])

			// Resolve CNAME entries
			if astRec.Type == "CNAME" && qtype != dns.TypeANY {
				if cnameRecords, err := pdb.ResolveCNAMEs(astRec.Content, qtype); err == nil {
					for _, r := range cnameRecords {
						matched = append(matched, r)
					}
				} else {
					return nil, err
				}
			}
		}
	}

	return matched, nil
}

func (pdb *PowerDNSGenericSQLBackend) SearchDomain(qname string) (*pdnsmodel.Domain, error) {
	if strings.HasSuffix(qname, ".") {
		qname = strings.TrimSuffix(qname, ".")
	}

	domainParts := dns.SplitDomainName(strings.ToLower(qname))
	var domainSearch []string

	for first := 0; first < len(domainParts)-1; first++ {
		domain := strings.Join(domainParts[first:], ".")
		domainSearch = append(domainSearch, domain)
	}

	var domainMatches []pdnsmodel.Domain
	var domainResult *pdnsmodel.Domain = nil
	domainLength := -1

	if err := pdb.Where(map[string]interface{}{"name": &domainSearch}).Find(&domainMatches).Error; err != nil {
		return nil, err
	}

	if len(domainMatches) > 0 {
		for matchIndex, domain := range domainMatches {
			if domainLength == -1 || len(domain.Name) > domainLength {
				domainResult = &domainMatches[matchIndex]
				domainLength = len(domain.Name)
			}
		}
	}

	return domainResult, nil
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
	len1 := len(l1)
	len2 := len(l2)
	asterisk := false
	match := true

	for last := 1; !asterisk && match && last <= len2; last++ {
		match = bool(last <= len2 && last <= len1)

		if match {
			// Everything matches after the asterisk
			asterisk = bool(l2[len2-last] == "*")

			if !asterisk {
				// Compare the Domain Parts
				match = bool(l1[len1-last] == l2[len2-last])
			}
		}
	}

	return match
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
