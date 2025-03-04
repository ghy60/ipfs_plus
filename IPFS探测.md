# IPFS 节点探测系统

## 项目概述

本项目是基于 IPFS Kubo 的节点探测系统，通过增强 IPFS 的节点发现机制，实现了节点信息的自动收集、存储和管理。系统支持双重节点发现方式（被动和主动），并通过 Kafka 消息队列将发现的节点信息持久化到 MySQL 数据库中，便于后续的分析和利用。同时，系统集成了地理位置解析功能，可以分析节点的地理分布情况。

### 主要功能

1. **双重节点发现机制**：同时支持被动发现和主动发现两种方式，最大化节点的发现效率
2. **全面的节点信息收集**：收集节点的详细信息，包括节点ID、地址、协议版本、代理版本和网络延迟等
3. **消息队列中转**：使用 Kafka 作为消息队列，提高系统可靠性和扩展性
4. **数据库持久化存储**：将发现的节点信息存储到 MySQL 数据库，支持事务处理和并发控制
5. **地理位置解析**：解析节点IP地址的地理位置信息，提供地区、国家、城市等维度的分析
6. **文件导出功能**：支持将发现的节点信息定期导出到文件，便于备份和分析
7. **错误处理和重试机制**：实现了错误处理和重试机制，特别是针对数据库死锁的处理

## 技术架构

### 整体架构

系统基于 IPFS Kubo 实现，主要扩展了节点发现、消息队列、数据持久化和地理位置解析模块：

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
         ↓
┌─────────────────────┐
│   地理位置解析服务   │
└─────────────────────┘
         ↓
    ┌────────────┐
    │ 地理位置数据 │
    └────────────┘
```

### 核心模块及文件

1. **节点发现模块**：实现了被动和主动两种节点发现方式
   - `kubo/core/node/libp2p/discovery.go`：节点发现处理的核心逻辑和被动发现实现
   - `kubo/core/node/libp2p/activediscovery.go`：主动节点发现的实现

2. **消息队列模块**：负责将节点信息发送到Kafka队列
   - `kubo/core/node/libp2p/node_exporter.go`：包含消息生产者的实现

3. **数据持久化模块**：负责从Kafka消费消息并将节点信息导出到数据库和文件
   - `kubo/core/node/libp2p/node_exporter.go`：同时包含Kafka消费者和数据库交互的实现

4. **地理位置解析模块**：解析节点IP地址的地理位置信息
   - `ip_geolocation.py`：IP地理位置解析服务
   - `ip_geolocation_adapter.py`：适配不同IP解析接口的适配器

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
    
    // 处理节点地址
    for _, addr := range nodeInfo.Addresses {
        _, err = tx.Exec(
            "INSERT INTO node_addresses (node_id, address) VALUES (?, ?) ON DUPLICATE KEY UPDATE last_seen=NOW()",
            nodeInfo.NodeID, addr,
        )
        if err != nil {
            return err
        }
    }
    
    // 提交事务
    return tx.Commit()
}
```

### 5. 地理位置解析服务

IP地理位置解析服务是一个独立的Python服务，它从数据库中读取未解析地理位置的IP地址，通过调用外部API或本地地理位置数据库获取地理位置信息，并将结果更新到数据库：

```python
# 位于 ip_geolocation.py
class IPGeolocationService:
    def __init__(self, db_config, api_key=None):
        """初始化地理位置解析服务"""
        self.db_config = db_config
        self.api_key = api_key
        self.db_conn = None
        self.adapter = self._get_adapter()
        
    def _get_adapter(self):
        """获取适合的地理位置API适配器"""
        if self.api_key:
            return PremiumGeoAdapter(self.api_key) 
        return FreeGeoAdapter()  # 免费API适配器
        
    def ensure_geo_columns(self):
        """确保数据库表有必要的地理位置列"""
        cursor = self.db_conn.cursor()
        try:
            # 检查并添加地理位置相关列
            for column, column_type in GEO_COLUMNS.items():
                cursor.execute(f"SHOW COLUMNS FROM node_addresses LIKE '{column}'")
                if cursor.fetchone() is None:
                    cursor.execute(f"ALTER TABLE node_addresses ADD COLUMN {column} {column_type}")
            self.db_conn.commit()
        finally:
            cursor.close()
            
    def process_unresolved_ips(self, batch_size=100):
        """处理未解析地理位置的IP地址"""
        cursor = self.db_conn.cursor()
        try:
            # 获取需要解析的IP列表
            cursor.execute("""
                SELECT address FROM node_addresses 
                WHERE geo_updated_at IS NULL 
                  AND address LIKE '%/ip4/%' 
                LIMIT %s
            """, (batch_size,))
            
            addresses = cursor.fetchall()
            for addr_row in addresses:
                # 提取IP地址并获取地理位置
                ip = self._extract_ip(addr_row[0])
                if ip and self._is_valid_public_ip(ip):
                    geo_data = self.adapter.get_geo_data(ip)
                    if geo_data:
                        self._update_geo_data(cursor, addr_row[0], geo_data)
                else:
                    # 标记为已处理但无有效数据
                    self._mark_as_processed(cursor, addr_row[0])
                    
            self.db_conn.commit()
        finally:
            cursor.close()
```

## Kafka 集成配置

系统使用 Kafka 作为中央消息队列，确保节点数据的可靠传输和处理。以下是关键的 Kafka 配置和集成点：

### Kafka 生产者配置

```go
// 位于 kubo/core/node/libp2p/node_exporter.go
func NewNodeExporter(brokers []string, topic string, exportInterval time.Duration) (*NodeExporter, error) {
    // 配置Kafka生产者
    config := sarama.NewConfig()
    config.Producer.RequiredAcks = sarama.WaitForAll
    config.Producer.Retry.Max = 5
    config.Producer.Return.Successes = true
    
    // 创建生产者
    producer, err := sarama.NewSyncProducer(brokers, config)
    if err != nil {
        return nil, err
    }
    
    return &NodeExporter{
        producer:       producer,
        topic:          topic,
        exportInterval: exportInterval,
    }, nil
}
```

### Kafka 消费者配置

```go
// 位于 kubo/core/node/libp2p/node_exporter.go
func NewNodeConsumer(ctx context.Context, brokers []string, topic, group string, dbConn *sql.DB) (*NodeConsumer, error) {
    // 配置Kafka消费者
    config := sarama.NewConfig()
    config.Consumer.Return.Errors = true
    config.Consumer.Offsets.Initial = sarama.OffsetNewest
    
    // 创建消费者组
    consumer, err := sarama.NewConsumerGroup(brokers, group, config)
    if err != nil {
        return nil, err
    }
    
    nc := &NodeConsumer{
        ctx:      ctx,
        consumer: consumer,
        db:       dbConn,
        topic:    topic,
    }
    
    // 启动消费者服务
    go nc.startConsumerGroup()
    
    return nc, nil
}
```

### Kafka 主题设计

系统使用以下 Kafka 主题：

1. **ipfs-nodes**：存储发现的节点基本信息
2. **ipfs-addresses**：存储节点地址信息
3. **ipfs-metrics**：存储节点的性能指标

每个主题都使用适当的分区数（默认为3）和副本因子（默认为2）来确保性能和可靠性。

## 部署指南

### 系统要求

- **操作系统**：Linux（推荐 Ubuntu 20.04 或更高版本）
- **Go 版本**：1.18 或更高
- **Python 版本**：3.8 或更高
- **数据库**：MySQL 8.0 或更高
- **消息队列**：Kafka 2.8.0 或更高
- **JRE**：Java Runtime Environment 11 或更高（Kafka 依赖）

### 安装步骤

1. **安装 IPFS Kubo**：
   ```bash
   git clone https://github.com/ghy60/ipfs_plus.git
   cd ipfs_plus
   make build
   ```

2. **配置 Kafka**：
   ```bash
   # 启动 Zookeeper
   bin/zookeeper-server-start.sh config/zookeeper.properties
   
   # 启动 Kafka
   bin/kafka-server-start.sh config/server.properties
   
   # 创建必要的主题
   bin/kafka-topics.sh --create --topic ipfs-nodes --bootstrap-server localhost:9092 --partitions 3 --replication-factor 2
   bin/kafka-topics.sh --create --topic ipfs-addresses --bootstrap-server localhost:9092 --partitions 3 --replication-factor 2
   bin/kafka-topics.sh --create --topic ipfs-metrics --bootstrap-server localhost:9092 --partitions 3 --replication-factor 2
   ```

3. **配置数据库**：
   ```sql
   CREATE DATABASE ipfs_discovery;
   USE ipfs_discovery;
   
   CREATE TABLE node_info (
       node_id VARCHAR(100) PRIMARY KEY,
       protocol_version VARCHAR(50),
       agent_version VARCHAR(100),
       discovery_type VARCHAR(20),
       latency BIGINT,
       first_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
   );
   
   CREATE TABLE node_addresses (
       id BIGINT AUTO_INCREMENT PRIMARY KEY,
       node_id VARCHAR(100),
       address TEXT,
       country VARCHAR(50),
       city VARCHAR(100),
       latitude DOUBLE,
       longitude DOUBLE,
       region VARCHAR(100),
       timezone VARCHAR(50),
       isp VARCHAR(200),
       geo_updated_at TIMESTAMP NULL,
       first_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
       INDEX (node_id),
       INDEX (geo_updated_at)
   );
   ```

4. **安装地理位置解析服务**：
   ```bash
   # 安装依赖
   pip3 install mysql-connector-python requests ipaddress

   # 启动服务
   python3 ip_geolocation.py
   ```

5. **启动 IPFS 节点**：
   ```bash
   ./ipfs daemon --enable-node-discovery=true --kafka-brokers=localhost:9092
   ```

### 性能优化

1. **硬件建议**：
   - CPU：至少4核
   - 内存：至少8GB
   - 磁盘：SSD，至少100GB

2. **网络优化**：
   - 增加最大连接数：`sysctl -w net.core.somaxconn=1024`
   - 优化TCP设置：`sysctl -w net.ipv4.tcp_fin_timeout=30`

3. **Kafka 调优**：
   - 增加分区数量以提高并发处理能力
   - 调整 `replica.fetch.max.bytes` 和 `message.max.bytes` 处理大量节点信息

4. **数据库调优**：
   - 优化索引设计
   - 增加连接池大小
   - 配置适当的事务隔离级别

### 监控与维护

1. **日志监控**：
   - IPFS 日志：`tail -f ~/.ipfs/logs/ipfs.log`
   - Kafka 日志：`tail -f logs/server.log`
   - 地理位置服务日志：`tail -f ip_geolocation.log`

2. **定期维护**：
   - 数据库备份：`mysqldump -u root -p ipfs_discovery > backup.sql`
   - 清理过期数据：设置定时任务删除较旧的节点信息
   - 日志轮转：`logrotate -f /etc/logrotate.d/ipfs`

## 节点收集效率分析

经过测试，在正常网络环境下，本系统的节点发现效率如下：

1. **初始阶段**（0-24小时）：可收集约2,000-5,000个节点
2. **增长阶段**（1-3天）：可收集约8,000-12,000个节点
3. **稳定阶段**（3-7天）：可收集约15,000-20,000个节点
4. **饱和阶段**（7天以上）：节点数量增长放缓，但质量提高，活跃节点比例增加

影响节点收集效率的主要因素：

1. **网络环境**：带宽、延迟和网络质量
2. **部署位置**：不同地区的节点发现效率有差异
3. **主动发现频率**：增加主动发现频率可提高发现效率，但会增加系统负载
4. **初始引导节点**：优质的初始引导节点可以显著提高发现效率

在实际部署中，建议从多个地理位置部署节点，以获得更全面的IPFS网络节点覆盖。 