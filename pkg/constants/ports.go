package constants

const (
	// MySQLPort is the port number for MySQL
	MySQLPort     = 3306
	MySQLPortName = "mysql"

	// MySQLXPort is the port number for MySQL XProtocol
	MySQLXPort     = 33060
	MySQLXPortName = "mysqlx"

	// MySQLAdminPort is the port number for MySQL Admin
	MySQLAdminPort     = 33062
	MySQLAdminPortName = "mysql-admin"

	// MySQLHealthPort is the port number to check readiness and liveness of mysqld.
	MySQLHealthPort     = 9081
	MySQLHealthPortName = "health"

	// AgentPort is the port number for agent container
	AgentPort     = 9080
	AgentPortName = "agent"

	// AgentMetricsPort is the port number for agent container
	AgentMetricsPort     = 8080
	AgentMetricsPortName = "agent-metrics"
)
