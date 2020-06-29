package moco

// MyConfTemplateParameters define parameters for a MySQL configuration template
type MyConfTemplateParameters struct {
	// ServerID is the value for server_id of MySQL configuration
	ServerID uint
	// AdminAddress is the value for admin_address of MySQL configuration
	AdminAddress string
}
