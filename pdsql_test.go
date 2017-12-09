package pdsql_test

import (
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"

	"github.com/jinzhu/gorm"
	"github.com/miekg/dns"
	"github.com/wenerme/wps/coredns/plugin/pdsql"
	"github.com/wenerme/wps/pdns/model"
	"golang.org/x/net/context"
)

func TestWhoami(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	p := pdsql.PowerDNSGenericSQLBackend{DB: db.Debug()}
	if err := p.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	p.DB.Create(&pdnsmodel.Record{
		Name:    "example.org",
		Type:    "A",
		Content: "192.168.1.1",
		Ttl:     3600,
	})

	tests := []struct {
		qname         string
		qtype         uint16
		expectedCode  int
		expectedReply []string // ownernames for the records in the additional section.
		expectedErr   error
	}{
		{
			qname:         "example.org.",
			qtype:         dns.TypeA,
			expectedCode:  dns.RcodeSuccess,
			expectedReply: []string{"example.org."},
			expectedErr:   nil,
		},
	}

	ctx := context.TODO()

	for i, tc := range tests {
		req := new(dns.Msg)
		req.SetQuestion(dns.Fqdn(tc.qname), tc.qtype)

		rec := dnstest.NewRecorder(&test.ResponseWriter{})
		code, err := p.ServeDNS(ctx, rec, req)

		if err != tc.expectedErr {
			t.Errorf("Test %d: Expected error %v, but got %v", i, tc.expectedErr, err)
		}
		if code != int(tc.expectedCode) {
			t.Errorf("Test %d: Expected status code %d, but got %d", i, tc.expectedCode, code)
		}
		if len(tc.expectedReply) != len(rec.Msg.Answer) {
			t.Errorf("Test %d: Expected status len %d, but got %d", i, len(tc.expectedReply), len(rec.Msg.Answer))
		}

		for i, expected := range tc.expectedReply {
			actual := rec.Msg.Answer[i].Header().Name
			if actual != expected {
				t.Errorf("Test %d: Expected answer %s, but got %s", i, expected, actual)
			}
		}
	}
}
