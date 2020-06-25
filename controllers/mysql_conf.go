package controllers

import (
	"fmt"
	"sort"
	"strings"
)

var (
	// Default options of mysqld section
	defaultMycnf = map[string]string{}

	constMycnf = map[string]map[string]string{
		"mysqld": {
			"datadir":          "/var/lib/mysql",
			"pid_file":         "/var/run/mysqld/mysqld.pid",
			"socket":           "/var/run/mysqld/mysqld.sock",
			"secure_file_priv": "NULL",

			// Disabling symbolic-links to prevent assorted security risks
			"symbolic_links": "0",
			"server_id":      "{{ .server_id }}",
			"admin_address":  "{{ .admin_address }}",
		},
		"client": {
			"port":   "3306",
			"socket": "/tmp/mysql.sock",
		},
	}
)

type mysqlConfGenerator struct {
	conf map[string]map[string]string
}

func (g *mysqlConfGenerator) mergeSection(section string, conf map[string]string) {
	if g.conf == nil {
		g.conf = make(map[string]map[string]string)
	}
	if _, ok := g.conf[section]; !ok {
		g.conf[section] = make(map[string]string)
	}
	for k, v := range conf {
		g.conf[section][normalizeConfKey(k)] = v
	}
}

func (g *mysqlConfGenerator) merge(conf map[string]map[string]string) {
	for k, v := range conf {
		g.mergeSection(k, v)
	}
}

func (g *mysqlConfGenerator) generate() string {
	// sort keys to generate reproducible my.cnf
	sections := make([]string, 0, len(g.conf))
	for sec := range g.conf {
		sections = append(sections, sec)
	}
	sort.Strings(sections)

	b := new(strings.Builder)
	for _, sec := range sections {
		fmt.Fprintf(b, "[%s]\n", sec)

		confSec := g.conf[sec]
		// sort keys to generate reproducible my.cnf
		confKeys := make([]string, 0, len(confSec))
		for k := range confSec {
			confKeys = append(confKeys, k)
		}
		sort.Strings(confKeys)

		for _, k := range confKeys {
			fmt.Fprintf(b, "%s = %s\n", k, confSec[k])
		}
	}
	return b.String()
}

func normalizeConfKey(k string) string {
	return strings.ReplaceAll(k, "-", "_")
}
