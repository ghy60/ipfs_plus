package config

type Discovery struct {
	MDNS MDNS
	// ExportPeers 控制是否将发现的节点信息导出到文件
	ExportPeers ExportPeers
}

type MDNS struct {
	Enabled bool
}

// ExportPeers 配置节点信息导出功能
type ExportPeers struct {
	// Enabled 是否启用节点信息导出功能
	Enabled bool
	// DumpFile 导出文件的路径，如果为空则使用默认路径 (kubo/peers.dump 或当前目录下的 peers.dump)
	DumpFile string
	// DumpInterval 导出间隔时间（以秒为单位），默认为 120 秒
	DumpInterval int
}
