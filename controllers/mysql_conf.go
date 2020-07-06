package controllers

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cybozu-go/moco"
	"github.com/go-logr/logr"
)

var (
	confKeyPrefixes = []string{
		"enable_",
		"disable_",
		"skip_",
	}

	// Default options of mysqld section
	defaultMycnf = map[string]string{
		"tmpdir":               moco.TmpPath,
		"innodb_tmpdir":        moco.TmpPath,
		"character_set_server": "utf8mb4",
		"collation_server":     "utf8mb4_unicode_ci",

		"default_time_zone": "+0:00",

		"back_log":            "900",
		"max_connections":     "100000",
		"max_connect_errors":  "10",
		"max_allowed_packet":  "1G",
		"max_heap_table_size": "64M",
		"sort_buffer_size":    "4M",
		"join_buffer_size":    "2M",
		"thread_cache_size":   "100",
		"wait_timeout":        "604800", // == 7 days
		"lock_wait_timeout":   "60",

		// The default setting causes MY-012102. Adjust table_open_cache to suppress MY-012102,
		// since　open_files_limit and table_open_cache are interdependent and those values are set dynamically
		// See https://dev.mysql.com/doc/refman/8.0/en/server-error-reference.html#error_er_ib_msg_277
		"table_open_cache":       "65536",
		"table_definition_cache": "65536", // mitigate a innodb table cache eviction.

		"transaction_isolation": "READ-COMMITTED",
		"tmp_table_size":        "64M",
		"slow_query_log":        "ON",
		"long_query_time":       "2",
		"log_error_verbosity":   "3",
		"log_slow_extra":        "ON",

		"max_sp_recursion_depth": "20",

		"print_identified_with_as_hex": "ON",

		"loose_binlog_transaction_compression": "ON", // It would reduce the size of binlog by a third, but only available in 8.0.20 or later.

		"information_schema_stats_expiry": "0", // No need to cache information_schema.tables values

		"disabled_storage_engines": "MyISAM",

		// INNODB Specific options
		"innodb_flush_method":                 "O_DIRECT",
		"innodb_lock_wait_timeout":            "60",
		"innodb_print_all_deadlocks":          "1",
		"innodb_online_alter_log_max_size":    "1073741824",
		"innodb_adaptive_hash_index":          "ON",
		"innodb_numa_interleave":              "ON",
		"innodb_buffer_pool_in_core_file":     "OFF", // It is rarely necessary to include a buffer pool in a core file.
		"innodb_log_file_size":                "800M",
		"innodb_log_files_in_group":           "2",
		"innodb_buffer_pool_dump_pct":         "100",
		"innodb_buffer_pool_dump_at_shutdown": "1",
		"innodb_buffer_pool_load_at_startup":  "0",

		// Optimized options for SSD
		"innodb_flush_neighbors":      "0",
		"innodb_random_read_ahead":    "false",
		"innodb_read_ahead_threshold": "0",
		"innodb_log_write_ahead_size": "0",
	}

	constMycnf = map[string]map[string]string{
		"mysqld": {
			"port":    "3306",
			"socket":  filepath.Join(moco.VarRunPath, "mysqld.sock"),
			"datadir": moco.MySQLDataPath,

			"skip_name_resolve": "ON",

			"log_error":           "/var/log/mysql/mysql.err",
			"slow_query_log_file": "/var/log/mysql/mysql.slow",

			"enforce_gtid_consistency": "ON", // This must be set before gtid_mode.
			"gtid_mode":                "ON",

			"temptable_use_mmap": "OFF", // Disable because there is a bug report, see https://bugs.mysql.com/bug.php?id=98739

			"mysqlx_port": "33060",
			"admin_port":  "33062",

			"pid_file":       filepath.Join(moco.VarRunPath, "mysqld.pid"),
			"symbolic_links": "OFF", // Disabling symbolic-links to prevent assorted security risks

			"server_id":     "{{ .ServerID }}",
			"admin_address": "{{ .AdminAddress }}",
		},
		"client": {
			"port":                  "3306",
			"socket":                filepath.Join(moco.VarRunPath, "mysqld.sock"),
			"default_character_set": "utf8mb4",
		},
		"mysql": {
			"auto_rehash":  "OFF",
			"init_command": `"SET autocommit=0"`,
		},
	}
)

type mysqlConfGenerator struct {
	conf map[string]map[string]string
	log  logr.Logger
}

func (g *mysqlConfGenerator) mergeSection(section string, conf map[string]string, warn bool) {
	if g.conf == nil {
		g.conf = make(map[string]map[string]string)
	}
	if _, ok := g.conf[section]; !ok {
		g.conf[section] = make(map[string]string)
	}
	for k, v := range conf {
		nk := normalizeConfKey(k)
		for _, kk := range listConfKeyVariations(nk) {
			if _, ok := g.conf[section][kk]; ok && warn {
				g.log.Info("overriding MySQL configuration", "key", kk)
			}
			delete(g.conf[section], kk)
		}
		g.conf[section][nk] = v
	}
}

func (g *mysqlConfGenerator) merge(conf map[string]map[string]string, warn bool) {
	for k, v := range conf {
		g.mergeSection(k, v, warn)
	}
}

func (g *mysqlConfGenerator) generate() (string, error) {
	// sort keys to generate reproducible my.cnf
	sections := make([]string, 0, len(g.conf))
	for sec := range g.conf {
		sections = append(sections, sec)
	}
	sort.Strings(sections)

	b := new(strings.Builder)
	for _, sec := range sections {
		_, err := fmt.Fprintf(b, "[%s]\n", sec)
		if err != nil {
			return "", err
		}

		confSec := g.conf[sec]
		// sort keys to generate reproducible my.cnf
		confKeys := make([]string, 0, len(confSec))
		for k := range confSec {
			confKeys = append(confKeys, k)
		}
		sort.Strings(confKeys)

		for _, k := range confKeys {
			_, err = fmt.Fprintf(b, "%s = %s\n", k, confSec[k])
			if err != nil {
				return "", err
			}
		}
	}
	return b.String(), nil
}

func normalizeConfKey(k string) string {
	return strings.ReplaceAll(k, "-", "_")
}

func listConfKeyVariations(k string) []string {
	base := strings.TrimPrefix(k, "loose_")
	for _, prefix := range confKeyPrefixes {
		base = strings.TrimPrefix(base, prefix)
	}

	variations := []string{base, "loose_" + base}
	for _, prefix := range confKeyPrefixes {
		variations = append(variations, prefix+base, "loose_"+prefix+base)
	}

	return variations
}
