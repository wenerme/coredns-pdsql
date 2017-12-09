package pdsql

import (
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/jinzhu/gorm"
	"github.com/mholt/caddy"
	"github.com/wenerme/wps/pdns/model"
)

func init() {
	caddy.RegisterPlugin("pdsql", caddy.Plugin{
		ServerType: "dns",
		Action:     setup,
	})
}

func setup(c *caddy.Controller) error {
	backend := PowerDNSGenericSQLBackend{}
	c.Next()
	if !c.NextArg() {
		return plugin.Error("pdsql", c.ArgErr())
	}
	dialect := c.Val()

	if !c.NextArg() {
		return plugin.Error("pdsql", c.ArgErr())
	}
	arg := c.Val()

	db, err := gorm.Open(dialect, arg)
	if err != nil {
		return err
	}
	backend.DB = db

	for c.NextBlock() {
		x := c.Val()
		switch x {
		case "debug":
			for _, v := range c.RemainingArgs() {
				switch v {
				case "db":
					backend.DB = backend.DB.Debug()
				}
			}
			backend.Debug = true
		case "auto-migrate":
			// currently only use records table
			if err := backend.AutoMigrate(); err != nil {
				return err
			}
		default:
			return plugin.Error("pdsql", c.Errf("unexpected '%v' command", x))
		}
	}

	dnsserver.
		GetConfig(c).
		AddPlugin(func(next plugin.Handler) plugin.Handler {
			return backend
		})

	return nil
}

func (self PowerDNSGenericSQLBackend) AutoMigrate() error {
	return self.DB.AutoMigrate(&pdnsmodel.Record{}).Error
}
