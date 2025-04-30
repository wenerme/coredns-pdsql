package pdsql_test

import (
	"fmt"
	"net"
	"testing"

	pdsql "github.com/wenerme/coredns-pdsql"
	"github.com/wenerme/coredns-pdsql/pdnsmodel"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"

	"github.com/glebarez/sqlite"
	"github.com/miekg/dns"
	"golang.org/x/net/context"
	"gorm.io/gorm"
)

type PowerDNSSQLTestCase struct {
	testName       string
	qname          string
	qtype          uint16
	expectedCode   int
	expectedType   []uint16
	expectedHeader []string // ownernames for the records in the answer section.
	expectedReply  []string // reply contents for the records in the answer section.
	expectedErr    error
	rrReply        []dns.RR
}

func TestPowerDNSSQL(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"))
	if err != nil {
		t.Fatal(err)
	}

	p := pdsql.PowerDNSGenericSQLBackend{DB: db.Debug()}
	if err := p.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	testDomains := map[string]*pdnsmodel.Domain{
		"example.org": &pdnsmodel.Domain{Name: "example.org", Type: "NATIVE"},
	}

	for _, d := range testDomains {
		if err := p.DB.Create(d).Error; err != nil {
			t.Fatal(err)
		}
	}

	testRecords := []pdnsmodel.Record{
		{Name: "example.org", DomainId: testDomains["example.org"].ID, Type: "A", Content: "192.168.1.1", Ttl: 3600},
		{Name: "example.org", DomainId: testDomains["example.org"].ID, Type: "AAAA", Content: "::ffff:c0a8:101", Ttl: 3600},
		{Name: "*.example.org", DomainId: testDomains["example.org"].ID, Type: "CNAME", Content: "example.org", Ttl: 3600},
		{Name: "cname1.example.org", DomainId: testDomains["example.org"].ID, Type: "CNAME", Content: "cname2.example.org", Ttl: 3600},
		{Name: "cname2.example.org", DomainId: testDomains["example.org"].ID, Type: "CNAME", Content: "example.org", Ttl: 3600},
		{Name: "nocase.example.org", DomainId: testDomains["example.org"].ID, Type: "CNAME", Content: "example.org", Ttl: 3600},
		{Name: "example.org", DomainId: testDomains["example.org"].ID, Type: "TXT", Content: "Example Response Text", Ttl: 3600},
		{Name: "multi.example.org", DomainId: testDomains["example.org"].ID, Type: "A", Content: "192.168.1.2", Ttl: 7200},
		{Name: "multi.example.org", DomainId: testDomains["example.org"].ID, Type: "A", Content: "192.168.1.3", Ttl: 7200},
		{Name: "example.org", DomainId: testDomains["example.org"].ID, Type: "MX", Content: "10 mail.example.org", Ttl: 3600},
		{Name: "example.org", DomainId: testDomains["example.org"].ID, Type: "MX", Content: "20 mail2.example.org", Ttl: 3600},
		{Name: "example.org", DomainId: testDomains["example.org"].ID, Type: "MX", Content: "mail3.example.org", Prio: 30, Ttl: 3600},
		{Name: "_xmpp._tcp.example.org", DomainId: testDomains["example.org"].ID, Type: "SRV", Content: "10 10 5269 example.org.", Ttl: 3600},
	}

	for _, r := range testRecords {
		if err := p.DB.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}

	tests := []PowerDNSSQLTestCase{
		{
			testName:       "Type A Request",
			qname:          "example.org.",
			qtype:          dns.TypeA,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeA},
			expectedHeader: []string{"example.org."},
			expectedReply:  []string{"192.168.1.1"},
			expectedErr:    nil,
			rrReply:        []dns.RR{&dns.A{A: net.ParseIP("192.168.1.1")}},
		},
		{
			testName:       "Type AAAA Request",
			qname:          "example.org.",
			qtype:          dns.TypeAAAA,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeAAAA},
			expectedHeader: []string{"example.org."},
			expectedReply:  []string{"::ffff:c0a8:101"},
			expectedErr:    nil,
			rrReply:        []dns.RR{&dns.AAAA{AAAA: net.ParseIP("::ffff:c0a8:101")}},
		},
		{
			testName:       "Type CNAME Request",
			qname:          "cname1.example.org.",
			qtype:          dns.TypeCNAME,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeCNAME, dns.TypeCNAME},
			expectedHeader: []string{"cname1.example.org.", "cname2.example.org."},
			expectedReply:  []string{"cname2.example.org.", "example.org."},
			expectedErr:    nil,
			rrReply:        []dns.RR{&dns.CNAME{Target: "cname2.example.org."}, &dns.CNAME{Target: "example.org."}},
		},
		{
			testName:       "CNAME resolves to A Record",
			qname:          "cname1.example.org.",
			qtype:          dns.TypeA,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeCNAME, dns.TypeCNAME, dns.TypeA},
			expectedHeader: []string{"cname1.example.org."},
			expectedReply:  []string{"cname2.example.org.", "cname1.example.org.", "192.168.1.1"},
			expectedErr:    nil,
			rrReply: []dns.RR{&dns.CNAME{Target: "cname2.example.org."},
				&dns.CNAME{Target: "example.org."}, &dns.A{A: net.ParseIP("192.168.1.1")}},
		},
		{
			testName:       "CNAME resolves to AAAA Record",
			qname:          "cname1.example.org.",
			qtype:          dns.TypeAAAA,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeCNAME, dns.TypeCNAME, dns.TypeAAAA},
			expectedHeader: []string{"cname1.example.org."},
			expectedReply:  []string{"cname2.example.org.", "cname1.example.org.", "::ffff:c0a8:101"},
			expectedErr:    nil,
			rrReply: []dns.RR{&dns.CNAME{Target: "cname2.example.org."},
				&dns.CNAME{Target: "example.org."}, &dns.A{A: net.ParseIP("::ffff:c0a8:101")}},
		},
		{
			testName:       "Case Insensitive Queries",
			qname:          "NoCase.Example.ORG.",
			qtype:          dns.TypeA,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeCNAME, dns.TypeA},
			expectedHeader: []string{"nocase.example.org."},
			expectedReply:  []string{"nocase.example.org.", "192.168.1.1"},
			expectedErr:    nil,
			rrReply: []dns.RR{
				&dns.CNAME{Target: "example.org."}, &dns.A{A: net.ParseIP("192.168.1.1")}},
		},
		{
			testName:       "Wildcard ANY Request",
			qname:          "NOT.Exists.Example.ORG.",
			qtype:          dns.TypeANY,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeCNAME},
			expectedHeader: []string{"not.exists.example.org."},
			expectedReply:  []string{"example.org."},
			expectedErr:    nil,
			rrReply: []dns.RR{
				&dns.CNAME{Target: "example.org."}},
		},
		{
			testName:       "Wildcard A Request",
			qname:          "NOT.Exists.Example.ORG.",
			qtype:          dns.TypeA,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeCNAME, dns.TypeA},
			expectedHeader: []string{"not.exists.example.org."},
			expectedReply:  []string{"example.org.", "192.168.1.1"},
			expectedErr:    nil,
			rrReply: []dns.RR{
				&dns.CNAME{Target: "example.org."}, &dns.A{A: net.ParseIP("192.168.1.1")}},
		},
		{
			testName:       "Wildcard AAAA Request",
			qname:          "NOT.Exists.Example.ORG.",
			qtype:          dns.TypeAAAA,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeCNAME, dns.TypeAAAA},
			expectedHeader: []string{"not.exists.example.org."},
			expectedReply:  []string{"example.org.", "::ffff:c0a8:101"},
			expectedErr:    nil,
			rrReply: []dns.RR{
				&dns.CNAME{Target: "example.org."}, &dns.A{A: net.ParseIP("::ffff:c0a8:101")}},
		},
		{
			testName:       "Type TXT Request",
			qname:          "example.org.",
			qtype:          dns.TypeTXT,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeTXT},
			expectedHeader: []string{"example.org."},
			expectedReply:  []string{"Example Response Text"},
			expectedErr:    nil,
			rrReply:        []dns.RR{&dns.TXT{Txt: []string{"Example Response Text"}}},
		},
		{
			testName:       "Type A Multi Request",
			qname:          "multi.example.org.",
			qtype:          dns.TypeA,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeA, dns.TypeA},
			expectedHeader: []string{"multi.example.org.", "multi.example.org."},
			expectedReply:  []string{"192.168.1.2", "192.168.1.3"},
			expectedErr:    nil,
			rrReply: []dns.RR{&dns.A{A: net.ParseIP("192.168.1.2")},
				&dns.A{A: net.ParseIP("192.168.1.3")}},
		},
		{
			testName:       "Type MX Request",
			qname:          "example.org",
			qtype:          dns.TypeMX,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeMX, dns.TypeMX, dns.TypeMX},
			expectedHeader: []string{"example.org.", "example.org.", "example.org."},
			expectedReply:  []string{"10 mail.example.org.", "20 mail2.example.org.", "30 mail3.example.org."},
			expectedErr:    nil,
			rrReply: []dns.RR{&dns.MX{Mx: "mail.example.org.", Preference: 10},
				&dns.MX{Mx: "mail2.example.org.", Preference: 20},
				&dns.MX{Mx: "mail3.example.org.", Preference: 30}},
		},
		{
			testName:       "Type SRV Request",
			qname:          "_xmpp._tcp.example.org.",
			qtype:          dns.TypeSRV,
			expectedCode:   dns.RcodeSuccess,
			expectedType:   []uint16{dns.TypeSRV},
			expectedErr:    nil,
			expectedHeader: []string{"_xmpp._tcp.example.org."},
			expectedReply:  []string{"10 10 5269 example.org."},
			rrReply:        []dns.RR{&dns.SRV{Target: "example.org.", Priority: 10, Weight: 10, Port: 5269}},
		},
	}

	ctx := context.TODO()

	for _, tc := range tests {
		req := new(dns.Msg)
		req.SetQuestion(dns.Fqdn(tc.qname), tc.qtype)

		observed := dnstest.NewRecorder(&test.ResponseWriter{})
		code, err := p.ServeDNS(ctx, observed, req)

		if err != tc.expectedErr {
			t.Errorf("Test '%s': Expected error %v, but got %v", tc.testName, tc.expectedErr, err)
		}
		if code != tc.expectedCode {
			t.Errorf("Test '%s': Expected status code %d, but got %d", tc.testName, tc.expectedCode, code)
		}

		if observed.Msg.Answer == nil {
			t.Errorf("Test '%s': Expected answer section, but got nil", tc.testName)
		}

		if len(tc.expectedReply) != len(observed.Msg.Answer) {
			t.Errorf("Test '%s': Expected status len %d, but got %d", tc.testName, len(tc.expectedReply), len(observed.Msg.Answer))
		}

		for i, answer := range observed.Msg.Answer {
			if answer.Header().Rrtype != tc.expectedType[i] {
				t.Errorf("Test '%s' - Header [%d]: Expected type %d, but got %d", tc.testName, i, tc.expectedType[i], answer.Header().Rrtype)
			}
		}

		for i, expected := range tc.expectedHeader {
			actual := observed.Msg.Answer[i].Header().Name
			if actual != expected {
				t.Errorf("Test '%s' - Answer [%d]: Expected answer %s, but got %s", tc.testName, i, expected, actual)
			}
		}

		for i, testExpected := range tc.rrReply {
			switch observed.Msg.Answer[i].(type) {
			case *dns.A:
				expectedRR := testExpected.(*dns.A)
				observedRR := observed.Msg.Answer[i].(*dns.A)

				if !expectedRR.A.Equal(observedRR.A) {
					t.Errorf("Test '%s' - Answer [%d]: Expected A reply %s, but got %s", tc.testName, i, expectedRR.A, observedRR.A)
				}

			case *dns.CNAME:
				expectedRR := testExpected.(*dns.CNAME)
				observedRR := observed.Msg.Answer[i].(*dns.CNAME)

				if expectedRR.Target != observedRR.Target {
					t.Errorf("Test '%s' - Answer [%d]: Expected CNAME reply %s, but got %s", tc.testName, i, expectedRR.Target, observedRR.Target)
				}

			case *dns.TXT:
				expectedRR := testExpected.(*dns.TXT)
				observedRR := observed.Msg.Answer[i].(*dns.TXT)

				if len(expectedRR.Txt) != len(observedRR.Txt) {
					t.Errorf("Test '%s' - Answer [%d]: Expected TXT reply of length %d, but got length %d", tc.testName, i, len(expectedRR.Txt), len(observedRR.Txt))
				}
				for ctr := range expectedRR.Txt {
					if expectedRR.Txt[ctr] != observedRR.Txt[ctr] {
						t.Errorf("Test '%s' - Answer [%d]: Expected TXT reply ctr=%d to be %s, but got %s", tc.testName, i, ctr, expectedRR.Txt[ctr], observedRR.Txt[ctr])
					}
				}

			case *dns.MX:
				expectedRR := testExpected.(*dns.MX)
				observedRR := observed.Msg.Answer[i].(*dns.MX)

				if expectedRR.Mx != observedRR.Mx {
					t.Errorf("Test '%s' - Answer [%d]: Expected MX reply %s, but got %s", tc.testName, i, expectedRR.Mx, observedRR.Mx)
					fmt.Printf("expected MX: %v\n", expectedRR.Mx)
				}

			case *dns.SRV:
				expectedRR := testExpected.(*dns.SRV)
				observedRR := observed.Msg.Answer[i].(*dns.SRV)

				if (expectedRR.Target != observedRR.Target) ||
					(expectedRR.Port != observedRR.Port) ||
					(expectedRR.Priority != observedRR.Priority) ||
					(expectedRR.Weight != observedRR.Weight) {

					t.Errorf(
						"Test '%s' - Answer [%d]: Expected SRV reply target=%s, priority=%d, weight=%d, port=%d, "+
							"but got target=%s, priority=%d, weight=%d, port=%d",
						tc.testName, i, expectedRR.Target, expectedRR.Priority, expectedRR.Weight, expectedRR.Port,
						observedRR.Target, observedRR.Priority, observedRR.Weight, observedRR.Port,
					)
				}
			}
		}
	}
}

func TestWildcardMatch(t *testing.T) {

	tests := []struct {
		pattern  string
		name     string
		expected bool
	}{
		{"*.example.org.", "example.org.", false},
		{"a.example.org.", "a.example.org.", true},
		{"*.example.org.", "a.example.org.", true},
		{"*.example.org.", "abcd.example.org.", true},
		{"*.example.org.", "not.exist.example.org.", true},
	}

	for i, tc := range tests {
		act := pdsql.WildcardMatch(tc.name, tc.pattern)
		if tc.expected != act {
			t.Errorf("Test %d: Expected  %v, but got %v", i, tc.expected, act)
		}
	}
}
