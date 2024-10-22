package pdsql_test

import (
	"fmt"
	"net"
	"testing"

	pdsql "github.com/wenerme/coredns-pdsql"
	"github.com/wenerme/coredns-pdsql/pdnsmodel"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"

	"github.com/jinzhu/gorm"
	"github.com/miekg/dns"
	"golang.org/x/net/context"
)

type PowerDNSSQLTestCase struct {
	qname          string
	qtype          uint16
	expectedCode   int
	expectedHeader []string // ownernames for the records in the answer section.
	expectedReply  []string // reply contents for the records in the answer section.
	expectedErr    error
	rrReply        []dns.RR
}

func TestPowerDNSSQL(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	p := pdsql.PowerDNSGenericSQLBackend{DB: db.Debug()}
	if err := p.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	testRecords := []*pdnsmodel.Record{
		{Name: "example.org", Type: "A", Content: "192.168.1.1", Ttl: 3600},
		{Name: "cname.example.org", Type: "CNAME", Content: "example.org.", Ttl: 3600},
		{Name: "example.org", Type: "TXT", Content: "Example Response Text", Ttl: 3600},
		{Name: "multi.example.org", Type: "A", Content: "192.168.1.2", Ttl: 7200},
		{Name: "multi.example.org", Type: "A", Content: "192.168.1.3", Ttl: 7200},
		{Name: "example.org", Type: "MX", Content: "10 mail.example.org.", Ttl: 3600},
		{Name: "example.org", Type: "MX", Content: "20 mail2.example.org.", Ttl: 3600},
		{Name: "_xmpp._tcp.example.org", Type: "SRV", Content: "10 10 5269 example.org.", Ttl: 3600},
	}

	for _, r := range testRecords {
		if err := p.DB.Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}

	tests := []PowerDNSSQLTestCase{
		{
			qname:          "example.org.",
			qtype:          dns.TypeA,
			expectedCode:   dns.RcodeSuccess,
			expectedHeader: []string{"example.org."},
			expectedReply:  []string{"192.168.1.1"},
			expectedErr:    nil,
			rrReply:        []dns.RR{&dns.A{A: net.ParseIP("192.168.1.1")}},
		},
		{
			qname:          "cname.example.org.",
			qtype:          dns.TypeCNAME,
			expectedCode:   dns.RcodeSuccess,
			expectedHeader: []string{"cname.example.org."},
			expectedReply:  []string{"example.org."},
			expectedErr:    nil,
			rrReply:        []dns.RR{&dns.CNAME{Target: "example.org."}},
		},
		{
			qname:          "example.org.",
			qtype:          dns.TypeTXT,
			expectedCode:   dns.RcodeSuccess,
			expectedHeader: []string{"example.org."},
			expectedReply:  []string{"Example Response Text"},
			expectedErr:    nil,
			rrReply:        []dns.RR{&dns.TXT{Txt: []string{"Example Response Text"}}},
		},
		{
			qname:          "multi.example.org.",
			qtype:          dns.TypeA,
			expectedCode:   dns.RcodeSuccess,
			expectedHeader: []string{"multi.example.org.", "multi.example.org."},
			expectedReply:  []string{"192.168.1.2", "192.168.1.3"},
			expectedErr:    nil,
			rrReply: []dns.RR{&dns.A{A: net.ParseIP("192.168.1.2")},
				&dns.A{A: net.ParseIP("192.168.1.3")}},
		},
		{
			qname:          "example.org",
			qtype:          dns.TypeMX,
			expectedCode:   dns.RcodeSuccess,
			expectedHeader: []string{"example.org.", "example.org."},
			expectedReply:  []string{"10 mail.example.org.", "20 mail2.example.org."},
			expectedErr:    nil,
			rrReply:        []dns.RR{&dns.MX{Mx: "mail.example.org.", Preference: 10}, &dns.MX{Mx: "mail2.example.org.", Preference: 20}},
		},
		{
			qname:          "_xmpp._tcp.example.org.",
			qtype:          dns.TypeSRV,
			expectedCode:   dns.RcodeSuccess,
			expectedErr:    nil,
			expectedHeader: []string{"_xmpp._tcp.example.org."},
			expectedReply:  []string{"10 10 5269 example.org."},
			rrReply:        []dns.RR{&dns.SRV{Target: "example.org.", Priority: 10, Weight: 10, Port: 5269}},
		},
	}

	ctx := context.TODO()

	for i, tc := range tests {
		req := new(dns.Msg)
		req.SetQuestion(dns.Fqdn(tc.qname), tc.qtype)

		observed := dnstest.NewRecorder(&test.ResponseWriter{})
		code, err := p.ServeDNS(ctx, observed, req)

		if err != tc.expectedErr {
			t.Errorf("Test %d: Expected error %v, but got %v", i, tc.expectedErr, err)
		}
		if code != tc.expectedCode {
			t.Errorf("Test %d: Expected status code %d, but got %d", i, tc.expectedCode, code)
		}

		if observed.Msg.Answer == nil {
			t.Errorf("Test %d: Expected answer section, but got nil", i)
		}

		if len(tc.expectedReply) != len(observed.Msg.Answer) {
			t.Errorf("Test %d: Expected status len %d, but got %d", i, len(tc.expectedReply), len(observed.Msg.Answer))
		}

		for _, answer := range observed.Msg.Answer {
			if answer.Header().Rrtype != tc.qtype {
				t.Errorf("Test %d: Expected type %d, but got %d", i, tc.qtype, answer.Header().Rrtype)
			}
		}

		for i, expected := range tc.expectedHeader {
			actual := observed.Msg.Answer[i].Header().Name
			if actual != expected {
				t.Errorf("Test %d: Expected answer %s, but got %s", i, expected, actual)
			}

		}

		for i, testExpected := range tc.rrReply {
			switch observed.Msg.Answer[i].(type) {
			case *dns.A:
				expectedRR := testExpected.(*dns.A)
				observedRR := observed.Msg.Answer[i].(*dns.A)

				if !expectedRR.A.Equal(observedRR.A) {
					t.Errorf("Test %d: Expected A reply %s, but got %s", i, expectedRR.A, observedRR.A)
				}

			case *dns.CNAME:
				expectedRR := testExpected.(*dns.CNAME)
				observedRR := observed.Msg.Answer[i].(*dns.CNAME)

				if expectedRR.Target != observedRR.Target {
					t.Errorf("Test %d: Expected CNAME reply %s, but got %s", i, expectedRR.Target, observedRR.Target)
				}

			case *dns.TXT:
				expectedRR := testExpected.(*dns.TXT)
				observedRR := observed.Msg.Answer[i].(*dns.TXT)

				if len(expectedRR.Txt) != len(observedRR.Txt) {
					t.Errorf("Test %d: Expected TXT reply of length %d, but got length %d", i, len(expectedRR.Txt), len(observedRR.Txt))
				}
				for ctr := range expectedRR.Txt {
					if expectedRR.Txt[ctr] != observedRR.Txt[ctr] {
						t.Errorf("Test %d: Expected TXT reply ctr=%d to be %s, but got %s", i, ctr, expectedRR.Txt[ctr], observedRR.Txt[ctr])
					}
				}

			case *dns.MX:
				expectedRR := testExpected.(*dns.MX)
				observedRR := observed.Msg.Answer[i].(*dns.MX)

				if expectedRR.Mx != observedRR.Mx {
					t.Errorf("Test %d: Expected MX reply %s, but got %s", i, expectedRR.Mx, observedRR.Mx)
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
						"Test %d: Expected SRV reply target=%s, priority=%d, weight=%d, port=%d, "+
							"but got target=%s, priority=%d, weight=%d, port=%d",
						i, expectedRR.Target, expectedRR.Priority, expectedRR.Weight, expectedRR.Port,
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
	}

	for i, tc := range tests {
		act := pdsql.WildcardMatch(tc.name, tc.pattern)
		if tc.expected != act {
			t.Errorf("Test %d: Expected  %v, but got %v", i, tc.expected, act)
		}
	}
}
