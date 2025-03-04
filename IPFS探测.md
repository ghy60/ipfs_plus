# IPFS 节点探测系统

## 项目概述

本项目是基于 IPFS Kubo 的节点探测系统，通过增强 IPFS 的节点发现机制，实现了节点信息的自动收集、存储和管理。系统支持双重节点发现方式（被动和主动），并通过 Kafka 消息队列将发现的节点信息持久化到 MySQL 数据库中，便于后续的分析和利用。

### 主要功能

1. **双重节点发现机制**：同时支持被动发现和主动发现两种方式，最大化节点的发现效率
2. **全面的节点信息收集**：收集节点的详细信息，包括节点ID、地址、协议版本、代理版本和网络延迟等
3. **消息队列中转**：使用 Kafka 作为消息队列，提高系统可靠性和扩展性
4. **数据库持久化存储**：将发现的节点信息存储到 MySQL 数据库，支持事务处理和并发控制
5. **文件导出功能**：支持将发现的节点信息定期导出到文件，便于备份和分析
6. **错误处理和重试机制**：实现了错误处理和重试机制，特别是针对数据库死锁的处理

## 技术架构

### 整体架构

系统基于 IPFS Kubo 实现，主要扩展了节点发现、消息队列和数据持久化模块：

```
┌─────────────────────────────────────┐
│            IPFS Kubo                │
├─────────────────────────────────────┤
│                                     │
│  ┌───────────────┐                  │
│  │  节点发现模块  │                  │
│  └───────────────┘                  │
│     ↑        ↑                      │
│     │        │                      │
│  ┌──────┐ ┌──────┐                  │
│  │被动发现│ │主动发现│                  │
│  └──────┘ └──────┘                  │
│        ↓                            │
│  ┌────────────┐                     │
│  │ 消息生产者  │                     │
│  └────────────┘                     │
│        ↓                            │
└────────┼────────────────────────────┘
         ↓
   ┌────────────┐    
   │ Kafka 队列 │    
   └────────────┘    
         ↓
┌─────────────────────┐
│     消息消费者       │
└─────────────────────┘
         ↓             ↓
    ┌────────────┐  ┌─────────────┐
    │ MySQL 数据库 │  │ 节点信息文件 │
    └────────────┘  └─────────────┘
```

### 核心模块及文件

1. **节点发现模块**：实现了被动和主动两种节点发现方式
   - `kubo/core/node/libp2p/discovery.go`：节点发现处理的核心逻辑和被动发现实现
   - `kubo/core/node/libp2p/activediscovery.go`：主动节点发现的实现

2. **消息队列模块**：负责将节点信息发送到Kafka队列
   - `kubo/core/node/libp2p/node_exporter.go`：包含消息生产者的实现

3. **数据持久化模块**：负责从Kafka消费消息并将节点信息导出到数据库和文件
   - `kubo/core/node/libp2p/node_exporter.go`：同时包含Kafka消费者和数据库交互的实现

## 核心功能实现

### 1. 被动节点发现

被动发现通过两种方式实现：

1. **MDNS 发现**：利用 MDNS 协议在局域网内自动发现节点
2. **网络连接事件监听**：监听网络连接事件，当新节点连接时自动收集信息

核心代码位于 `discovery.go` 的 `SetupDiscovery` 函数：

```go
// 位于 kubo/core/node/libp2p/discovery.go
func SetupDiscovery(useMdns bool) func(helpers.MetricsCtx, fx.Lifecycle, host.Host, *discoveryHandler) error {
    return func(mctx helpers.MetricsCtx, lc fx.Lifecycle, host host.Host, handler *discoveryHandler) error {
        // MDNS 被动发现
        if useMdns {
            service := mdns.NewMdnsService(host, mdns.ServiceName, handler)
            if err := service.Start(); err != nil {
                log.Error("error starting mdns service: ", err)
                return nil
            }
        }

        // 网络连接事件监听
        host.Network().Notify(&network.NotifyBundle{
            ConnectedF: func(net network.Network, conn network.Conn) {
                // 收集并导出节点信息
                // ...
            },
        })
        
        // ...
    }
}
```

### 2. 主动节点发现

主动发现是定期扫描已知节点和尝试连接未连接节点的过程：

1. 每隔一定时间（默认10分钟）主动扫描网络
2. 处理当前已连接的节点，收集并导出信息
3. 尝试连接到一些尚未连接的节点
4. 将所有发现的节点信息发送到Kafka消息队列

核心代码位于 `activediscovery.go` 的 `discoverPeers` 函数：

```go
// 位于 kubo/core/node/libp2p/activediscovery.go
func discoverPeers(ctx context.Context, host host.Host, disc *discoveryHandler, exporter *NodeExporter) {
    // 获取当前连接的节点
    currentPeers := len(host.Network().Peers())
    
    // 处理已连接节点
    for _, p := range host.Network().Peers() {
        // 收集节点信息
        addrInfo := host.Peerstore().PeerInfo(p)
        // 获取节点元数据
        // ...
        // 导出节点信息（内部会发送到Kafka）
        exporter.ExportNode(ctx, addrInfo, "active", protoVer, agentVer, int64(latency))
        // ...
    }
    
    // 尝试连接未连接节点
    allPeers := host.Peerstore().Peers()
    for _, pid := range allPeers {
        if host.Network().Connectedness(pid) != 2 { // 2表示Connected
            // 尝试连接并导出信息
            // ...
        }
    }
}
```

### 3. 消息队列与节点信息处理

系统使用Kafka作为消息队列，实现了生产者-消费者模式处理节点信息：

1. 生产者：当发现新节点时，将节点信息序列化后发送到Kafka主题
2. 消费者：从Kafka主题中消费消息，并将数据写入数据库

核心代码位于 `node_exporter.go`：

```go
// 位于 kubo/core/node/libp2p/node_exporter.go

// 1. 消息生产者部分
func (ne *NodeExporter) ExportNode(ctx context.Context, p peer.AddrInfo, discoveryType, protoVer, agentVer string, latency int64) error {
    // 构建节点信息
    nodeInfo := NodeInfo{
        NodeID:          p.ID.String(),
        ProtocolVersion: protoVer,
        AgentVersion:    agentVer,
        DiscoveryType:   discoveryType,
        Latency:         latency,
    }
    
    // 序列化为JSON
    data, err := json.Marshal(nodeInfo)
    if err != nil {
        return err
    }
    
    // 发送到Kafka
    _, _, err = ne.producer.SendMessage(&sarama.ProducerMessage{
        Topic: "ipfs-nodes",
        Value: sarama.StringEncoder(data),
    })
    
    return err
}

// 2. 消息消费者部分
func (ne *NodeConsumer) startConsumer() {
    // 从Kafka消费消息
    for {
        select {
        case msg := <-ne.consumer.Messages():
            if err := ne.processMessage(msg); err != nil {
                log.Errorf("Failed to process message: %v", err)
            }
            ne.consumer.MarkOffset(msg, "")
        case <-ne.ctx.Done():
            return
        }
    }
}

// 3. 消息处理
func (ne *NodeConsumer) processMessage(msg *sarama.ConsumerMessage) error {
    maxRetries := 3
    for attempt := 0; attempt < maxRetries; attempt++ {
        err := ne.processMessageWithTransaction(msg)
        if err == nil {
            return nil
        }
        
        // 检查是否是死锁错误
        if strings.Contains(err.Error(), "Deadlock found") {
            log.Warnf("Database deadlock detected, retry attempt %d of %d", attempt+1, maxRetries)
            time.Sleep(time.Millisecond * 500 * time.Duration(attempt+1))
            continue
        }
        
        return err
    }
    
    return fmt.Errorf("Max retries reached, failed to process message")
}
```

### 4. 错误处理和重试机制

系统实现了针对数据库操作的错误处理和重试机制，特别是针对死锁的处理：

```go
// 位于 kubo/core/node/libp2p/node_exporter.go
func (ne *NodeConsumer) processMessageWithTransaction(msg *sarama.ConsumerMessage) error {
    // 解析消息
    var nodeInfo NodeInfo
    if err := json.Unmarshal(msg.Value, &nodeInfo); err != nil {
        return err
    }
    
    // 开始数据库事务
    tx, err := ne.db.BeginTx(ne.ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    
    // 插入节点信息
    _, err = tx.Exec(
        "INSERT INTO node_info (node_id, protocol_version, agent_version, discovery_type, latency) VALUES (?, ?, ?, ?, ?) "+
        "ON DUPLICATE KEY UPDATE protocol_version=?, agent_version=?, discovery_type=?, latency=?, last_seen=NOW()",
        nodeInfo.NodeID, nodeInfo.ProtocolVersion, nodeInfo.AgentVersion, nodeInfo.DiscoveryType, nodeInfo.Latency,
        nodeInfo.ProtocolVersion, nodeInfo.AgentVersion, nodeInfo.DiscoveryType, nodeInfo.Latency,
    )
    if err != nil {
        return err
    }
    
    // 处理地址信息
    // ...
    
    // 提交事务
    return tx.Commit()
}
```

## 数据库结构

系统使用两个主要的数据库表来存储节点信息：

### 1. node_info 表

存储节点的基本信息：

```sql
CREATE TABLE IF NOT EXISTS node_info (
    node_id VARCHAR(255) PRIMARY KEY,
    protocol_version VARCHAR(50),
    agent_version VARCHAR(100),
    discovery_type VARCHAR(20),
    latency BIGINT,
    last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_discovery_type (discovery_type),
    INDEX idx_last_seen (last_seen)
);
```

### 2. address_info 表

存储节点的地址信息：

```sql
CREATE TABLE IF NOT EXISTS address_info (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    node_id VARCHAR(255),
    multiaddr TEXT,
    last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (node_id) REFERENCES node_info(node_id) ON DELETE CASCADE,
    INDEX idx_node_id (node_id),
    INDEX idx_last_seen (last_seen)
);
```

## 系统配置

系统配置可以通过 IPFS 的配置文件进行调整，主要配置项包括：

```json
{
  "Discovery": {
    "ExportPeers": {
      "Enabled": true,
      "DumpFile": "/path/to/peers.dump",
      "DumpInterval": 120
    }
  },
  "Database": {
    "Host": "42.194.236.54",
    "Port": 3306,
    "User": "root",
    "Password": "As984315#",
    "Database": "ipfs_nodes",
    "MaxOpenConns": 10,
    "MaxIdleConns": 5
  },
  "Kafka": {
    "Brokers": ["kafka1:9092", "kafka2:9092", "kafka3:9092"],
    "Topic": "ipfs-nodes",
    "ConsumerGroup": "ipfs-node-consumer",
    "ClientID": "ipfs-node-client"
  }
}
```

## 部署流程

### 1. 环境准备

#### 1.1 系统要求

- 操作系统：Linux（推荐 Ubuntu 20.04 或更高版本）
- 内存：建议 6GB 以上
- CPU：建议 2 核以上
- 磁盘：根据节点数据规模，至少 20GB 可用空间
- 数据库：MySQL 5.7 或更高版本
- 消息队列：Kafka 2.x 或更高版本

#### 1.2 安装依赖

```bash
# 安装 Go
wget https://golang.org/dl/go1.16.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.16.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# 安装 IPFS 依赖
sudo apt-get update
sudo apt-get install -y git build-essential
```

### 2. 编译安装

```bash
# 克隆代码库
git clone https://github.com/ipfs/kubo.git
cd kubo

# 编译
make build

# 安装
make install
```

### 3. 安装和配置 Kafka

```bash
# 安装Java
sudo apt-get install -y openjdk-11-jdk

# 下载Kafka
wget https://archive.apache.org/dist/kafka/2.8.1/kafka_2.13-2.8.1.tgz
tar -xzf kafka_2.13-2.8.1.tgz
cd kafka_2.13-2.8.1

# 启动ZooKeeper服务（Kafka依赖）
bin/zookeeper-server-start.sh -daemon config/zookeeper.properties

# 启动Kafka服务
bin/kafka-server-start.sh -daemon config/server.properties

# 创建主题
bin/kafka-topics.sh --create --topic ipfs-nodes --bootstrap-server localhost:9092 --partitions 3 --replication-factor 1

# 验证主题创建成功
bin/kafka-topics.sh --describe --topic ipfs-nodes --bootstrap-server localhost:9092
```

### 4. 数据库配置

```bash
# 安装 MySQL
sudo apt-get install -y mysql-server

# 创建数据库
mysql -h localhost -u root -p
```

在 MySQL 中执行：

```sql
CREATE DATABASE ipfs_nodes;
USE ipfs_nodes;

-- 创建节点信息表
CREATE TABLE IF NOT EXISTS node_info (
    node_id VARCHAR(255) PRIMARY KEY,
    protocol_version VARCHAR(50),
    agent_version VARCHAR(100),
    discovery_type VARCHAR(20),
    latency BIGINT,
    last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_discovery_type (discovery_type),
    INDEX idx_last_seen (last_seen)
);

-- 创建地址信息表
CREATE TABLE IF NOT EXISTS address_info (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    node_id VARCHAR(255),
    multiaddr TEXT,
    last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (node_id) REFERENCES node_info(node_id) ON DELETE CASCADE,
    INDEX idx_node_id (node_id),
    INDEX idx_last_seen (last_seen)
);

-- 创建用户并授权
CREATE USER 'ipfs_user'@'%' IDENTIFIED BY 'your_password';
GRANT ALL PRIVILEGES ON ipfs_nodes.* TO 'ipfs_user'@'%';
FLUSH PRIVILEGES;
```

### 5. IPFS 初始化与配置

```bash
# 初始化 IPFS
ipfs init

# 配置数据库和Kafka连接
cat << EOF > ~/.ipfs/config
{
  "Discovery": {
    "ExportPeers": {
      "Enabled": true,
      "DumpFile": "~/kubo/peers.dump",
      "DumpInterval": 120
    }
  },
  "Database": {
    "Host": "localhost",
    "Port": 3306,
    "User": "ipfs_user",
    "Password": "your_password",
    "Database": "ipfs_nodes",
    "MaxOpenConns": 10,
    "MaxIdleConns": 5
  },
  "Kafka": {
    "Brokers": ["localhost:9092"],
    "Topic": "ipfs-nodes",
    "ConsumerGroup": "ipfs-node-consumer",
    "ClientID": "ipfs-node-client"
  }
}
EOF
```

### 6. 创建系统服务

#### 6.1 IPFS服务

```bash
# 创建 systemd 服务文件
sudo cat << EOF > /etc/systemd/system/ipfs.service
[Unit]
Description=IPFS Daemon Service
After=network.target
Requires=zookeeper.service kafka.service

[Service]
Type=simple
User=root
WorkingDirectory=/root
ExecStart=/usr/local/bin/ipfs daemon
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
```

#### 6.2 Kafka和ZooKeeper服务

```bash
# 创建ZooKeeper服务
sudo cat << EOF > /etc/systemd/system/zookeeper.service
[Unit]
Description=Apache ZooKeeper Service
After=network.target

[Service]
Type=forking
User=root
WorkingDirectory=/path/to/kafka_2.13-2.8.1
ExecStart=/path/to/kafka_2.13-2.8.1/bin/zookeeper-server-start.sh -daemon /path/to/kafka_2.13-2.8.1/config/zookeeper.properties
ExecStop=/path/to/kafka_2.13-2.8.1/bin/zookeeper-server-stop.sh
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

# 创建Kafka服务
sudo cat << EOF > /etc/systemd/system/kafka.service
[Unit]
Description=Apache Kafka Service
After=network.target zookeeper.service
Requires=zookeeper.service

[Service]
Type=forking
User=root
WorkingDirectory=/path/to/kafka_2.13-2.8.1
ExecStart=/path/to/kafka_2.13-2.8.1/bin/kafka-server-start.sh -daemon /path/to/kafka_2.13-2.8.1/config/server.properties
ExecStop=/path/to/kafka_2.13-2.8.1/bin/kafka-server-stop.sh
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

# 启用并启动服务
sudo systemctl daemon-reload
sudo systemctl enable zookeeper
sudo systemctl enable kafka
sudo systemctl enable ipfs
sudo systemctl start zookeeper
sudo systemctl start kafka
sudo systemctl start ipfs
```

### 7. 验证部署

```bash
# 检查ZooKeeper和Kafka状态
sudo systemctl status zookeeper
sudo systemctl status kafka

# 检查IPFS守护进程状态
sudo systemctl status ipfs

# 查看IPFS日志
journalctl -u ipfs -f

# 检查Kafka主题
/path/to/kafka_2.13-2.8.1/bin/kafka-console-consumer.sh --topic ipfs-nodes --bootstrap-server localhost:9092 --from-beginning

# 检查数据库中的节点信息
mysql -h localhost -u ipfs_user -p -e "SELECT COUNT(*) FROM ipfs_nodes.node_info;"
```

### 8. 监控与维护

#### 8.1 定期备份数据库

```bash
# 创建备份脚本
cat << EOF > /root/backup_ipfs_db.sh
#!/bin/bash
BACKUP_DIR="/root/ipfs_backups"
mkdir -p \$BACKUP_DIR
DATE=\$(date +%Y%m%d_%H%M%S)
mysqldump -h localhost -u ipfs_user -p'your_password' ipfs_nodes > \$BACKUP_DIR/ipfs_nodes_\$DATE.sql
find \$BACKUP_DIR -name "ipfs_nodes_*.sql" -mtime +7 -delete
EOF

chmod +x /root/backup_ipfs_db.sh

# 添加到 crontab
echo "0 2 * * * /root/backup_ipfs_db.sh" | crontab -
```

#### 8.2 定期清理过期数据

```sql
-- 创建清理过期数据的存储过程
DELIMITER //
CREATE PROCEDURE clean_old_nodes()
BEGIN
    -- 删除30天前的节点数据
    DELETE FROM node_info WHERE last_seen < DATE_SUB(NOW(), INTERVAL 30 DAY);
END //
DELIMITER ;

-- 添加到事件调度器
CREATE EVENT clean_nodes_event
ON SCHEDULE EVERY 1 DAY
DO CALL clean_old_nodes();
```

#### 8.3 Kafka日志清理

```bash
# 创建Kafka日志清理脚本
cat << EOF > /root/clean_kafka_logs.sh
#!/bin/bash
# 设置保留日志的天数
/path/to/kafka_2.13-2.8.1/bin/kafka-configs.sh --bootstrap-server localhost:9092 --alter --entity-type topics --entity-name ipfs-nodes --add-config retention.ms=604800000
EOF

chmod +x /root/clean_kafka_logs.sh

# 添加到crontab
echo "0 1 * * 0 /root/clean_kafka_logs.sh" | crontab -
```

## 故障排除

### 1. IPFS 无法启动

检查是否存在锁文件：
```bash
rm -f ~/.ipfs/repo.lock
```

### 2. Kafka 相关问题

如果 Kafka 无法启动或消息无法正常生产/消费：

```bash
# 检查Kafka和ZooKeeper日志
journalctl -u kafka -n 100
journalctl -u zookeeper -n 100

# 重启Kafka服务
sudo systemctl restart zookeeper
sudo systemctl restart kafka

# 验证主题是否存在
/path/to/kafka_2.13-2.8.1/bin/kafka-topics.sh --list --bootstrap-server localhost:9092
```

### 3. 数据库连接失败

检查数据库配置和网络连接：
```bash
mysql -h [数据库主机] -u [用户名] -p -e "SELECT 1;"
```

### 4. 数据库死锁

已实现自动重试机制，如果问题严重，可以尝试优化数据库配置：
```sql
SET GLOBAL innodb_lock_wait_timeout = 120;
```

### 5. 内存不足

调整 IPFS 配置以减少内存使用：
```bash
# 编辑 ~/.ipfs/config 文件，调整以下参数
"Datastore": {
  "StorageMax": "10GB",
  "GCPeriod": "1h"
}
```

### 6. 消费者处理消息失败

如果消费者处理消息失败，可以查看日志并检查消费者组状态：

```bash
# 查看消费者组状态
/path/to/kafka_2.13-2.8.1/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --group ipfs-node-consumer

# 如果需要，重置消费者组偏移量
/path/to/kafka_2.13-2.8.1/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --group ipfs-node-consumer --reset-offsets --to-earliest --execute --topic ipfs-nodes
```

## 总结

本项目通过增强 IPFS 的节点发现机制，并结合 Kafka 消息队列和 MySQL 数据库，实现了节点信息的可靠收集、处理和存储。采用生产者-消费者模式不仅提高了系统的可靠性，还增强了系统的扩展性和容错能力。

系统架构采用了以下关键技术：
1. IPFS 节点发现机制（主动和被动）用于发现网络节点
2. Kafka 消息队列用于消息缓冲和解耦
3. 消费者模式将节点信息导入 MySQL 数据库
4. 事务管理和重试机制保证数据一致性

系统的部署涉及多个组件的配置，包括 IPFS、Kafka、ZooKeeper 和 MySQL 数据库。通过本文档提供的详细部署流程，可以快速搭建一个完整的节点探测系统，用于 IPFS 网络分析和研究。 