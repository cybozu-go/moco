package mycnf

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cybozu-go/moco/pkg/constants"
)

var (
	// Default options of mysqld section
	defaultMycnf = map[string]string{
		"tmpdir":               constants.TmpPath,
		"innodb_tmpdir":        constants.TmpPath,
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

		// This would reduce the size of binlog by a third.
		// Available since MySQL 8.0.20
		"loose_binlog_transaction_compression": "ON",

		// Enabling this would take long time at startup if there are a lot of tables.
		// Available since MySQL 8.0.21
		"loose_innodb_validate_tablespace_paths": "OFF",

		// Disabled because of https://bugs.mysql.com/bug.php?id=98739
		// Fixed in MySQL 8.0.21
		"temptable_use_mmap": "OFF",

		// No need to cache information_schema.tables values
		"information_schema_stats_expiry": "0",

		"disabled_storage_engines": "MyISAM",

		// InnoDB Specific options
		"innodb_flush_method":                 "O_DIRECT",
		"innodb_lock_wait_timeout":            "60",
		"innodb_print_all_deadlocks":          "1",
		"innodb_online_alter_log_max_size":    "1073741824",
		"innodb_adaptive_hash_index":          "ON",
		"loose_innodb_numa_interleave":        "ON",
		"innodb_buffer_pool_in_core_file":     "OFF", // It is rarely necessary to include a buffer pool in a core file.
		"innodb_log_file_size":                "800M",
		"innodb_log_files_in_group":           "2",
		"innodb_buffer_pool_dump_pct":         "100",
		"innodb_buffer_pool_dump_at_shutdown": "1",
		"innodb_buffer_pool_load_at_startup":  "0",

		// Optimization options for SSD
		"innodb_flush_neighbors":      "0",
		"innodb_random_read_ahead":    "false",
		"innodb_read_ahead_threshold": "0",
		"innodb_log_write_ahead_size": "0",
	}

	constMycnf = map[string]map[string]string{
		"mysqld": {
			"port":             strconv.Itoa(constants.MySQLPort),
			"socket":           filepath.Join(constants.RunPath, "mysqld.sock"),
			"datadir":          constants.MySQLDataPath,
			"secure_file_priv": "NULL",

			"skip_name_resolve": "ON",

			"log_error":           filepath.Join(constants.LogDirPath, constants.MySQLErrorLogName),
			"slow_query_log_file": filepath.Join(constants.LogDirPath, constants.MySQLSlowLogName),

			"enforce_gtid_consistency": "ON", // This must be set before gtid_mode.
			"gtid_mode":                "ON",
			"relay_log_recovery":       "OFF", // Turning this on would risk the loss of transaction in case of chained failures

			"mysqlx_port": strconv.Itoa(constants.MySQLXPort),
			"admin_port":  strconv.Itoa(constants.MySQLAdminPort),

			"pid_file":       filepath.Join(constants.RunPath, "mysqld.pid"),
			"symbolic_links": "OFF", // Disabling symbolic-links to prevent assorted security risks

			"read_only":        "ON",
			"super_read_only":  "ON",
			"skip_slave_start": "ON",

			"loose_rpl_semi_sync_master_timeout": strconv.Itoa(24 * 60 * 60 * 1000),
		},
		"client": {
			"port":                        strconv.Itoa(constants.MySQLPort),
			"socket":                      filepath.Join(constants.RunPath, "mysqld.sock"),
			"loose_default_character_set": "utf8mb4",
		},
		"mysql": {
			"auto_rehash":  "OFF",
			"init_command": `"SET autocommit=0"`,
		},
	}
)

func Generate(mysqldConf map[string]string) string {
	conf := make(map[string]map[string]string)

	conf["mysqld"] = mergeSection(defaultMycnf, mysqldConf)

	for sec, secConf := range constMycnf {
		conf[sec] = mergeSection(conf[sec], secConf)
	}

	// sort keys to generate reproducible my.cnf
	sections := make([]string, 0, len(conf))
	for sec := range conf {
		sections = append(sections, sec)
	}
	sort.Strings(sections)

	b := new(strings.Builder)
	for _, sec := range sections {
		_, err := fmt.Fprintf(b, "[%s]\n", sec)
		if err != nil {
			panic(err)
		}

		confSec := conf[sec]
		// sort keys to generate reproducible my.cnf
		confKeys := make([]string, 0, len(confSec))
		for k := range confSec {
			confKeys = append(confKeys, k)
		}
		sort.Strings(confKeys)

		for _, k := range confKeys {
			_, err = fmt.Fprintf(b, "%s = %s\n", k, confSec[k])
			if err != nil {
				panic(err)
			}
		}

		fmt.Fprintf(b, "\n")
	}

	_, err := fmt.Fprintf(b, "!includedir %s\n", constants.MySQLInitConfPath)
	if err != nil {
		panic(err)
	}

	return b.String()
}

func mergeSection(conf1, conf2 map[string]string) map[string]string {
	conf := make(map[string]string)

	for k, v := range conf1 {
		nk := normalizeConfKey(k)
		conf[nk] = v
	}

	for k, v := range conf2 {
		nk := normalizeConfKey(k)
		for _, kk := range listConfKeyVariations(nk) {
			delete(conf, kk)
		}
		conf[nk] = v
	}

	return conf
}

func normalizeConfKey(k string) string {
	return strings.ReplaceAll(k, "-", "_")
}

func listConfKeyVariations(k string) []string {
	base := strings.TrimPrefix(k, "loose_")
	return []string{base, "loose_" + base}
}
