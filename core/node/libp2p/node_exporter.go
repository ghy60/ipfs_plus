package libp2p

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "time"
    "errors"
    "strings"
    "sync"
    
    "github.com/IBM/sarama"
    _ "github.com/go-sql-driver/mysql"
    "github.com/libp2p/go-libp2p/core/peer"
)

type AddressInfo struct {
    Protocol    string `json:"protocol"`    // ip4, ip6, dns4, dns6 等
    Host        string `json:"host"`        // IP地址或域名
    Port        int    `json:"port"`        // 端口号
    Transport   string `json:"transport"`   // tcp, udp, quic 等
    Version     string `json:"version"`     // quic-v1 等版本信息
}

type NodeInfo struct {
    //====================GHY======================//
    // 将peer_id的JSON标签改为node_id，以匹配数据库字段
    PeerID          string        `json:"node_id"`
    //====================GHY======================//
    FirstSeen       time.Time     `json:"first_seen"`
    LastSeen        time.Time     `json:"last_seen"`
    ConnectionType  string        `json:"connection_type"`
    ProtocolVersion string        `json:"protocol_version"`
    AgentVersion    string        `json:"agent_version"`
    Latency         int64         `json:"latency"`
    Addresses       []AddressInfo `json:"addresses"`
}

type NodeExporter struct {
    kafkaProducer sarama.SyncProducer
    topic         string
    consumer      *NodeConsumer
    wg            sync.WaitGroup
    stopCh        chan struct{}
}

type NodeConsumer struct {
    consumer sarama.Consumer
    db       *sql.DB
    topic    string
    ready    chan bool
    stopCh   chan struct{}
}

func parseMultiaddr(addr string) (AddressInfo, error) {
    parts := strings.Split(addr, "/")
    info := AddressInfo{}
    
    for i := 0; i < len(parts); i++ {
        if parts[i] == "" {
            continue
        }
        
        switch parts[i] {
        case "ip4", "ip6", "dns4", "dns6":
            info.Protocol = parts[i]
            if i+1 < len(parts) {
                info.Host = parts[i+1]
                i++
            }
        case "tcp", "udp":
            info.Transport = parts[i]
            if i+1 < len(parts) {
                port := 0
                fmt.Sscanf(parts[i+1], "%d", &port)
                info.Port = port
                i++
            }
        case "quic":
            info.Transport = parts[i]
        case "quic-v1":
            info.Transport = "quic"
            info.Version = "v1"
        }
    }
    
    return info, nil
}

//====================GHY======================//
// 数据库连接重试逻辑，提高连接稳定性
func connectWithRetry(dsn string, maxRetries int) (*sql.DB, error) {
    var db *sql.DB
    var err error
    
    for i := 0; i < maxRetries; i++ {
        db, err = sql.Open("mysql", dsn)
        if err != nil {
            fmt.Printf("Failed to open database connection (attempt %d/%d): %v\n", i+1, maxRetries, err)
            time.Sleep(time.Second * 2)
            continue
        }

        // 测试连接
        err = db.Ping()
        if err == nil {
            return db, nil
        }
        
        fmt.Printf("Failed to ping database (attempt %d/%d): %v\n", i+1, maxRetries, err)
        db.Close()
        time.Sleep(time.Second * 2)
    }
    
    return nil, fmt.Errorf("failed to connect to database after %d attempts: %v", maxRetries, err)
}
//====================GHY======================//

func NewNodeExporter() (*NodeExporter, error) {
    // 配置Kafka生产者
    config := sarama.NewConfig()
    config.Producer.RequiredAcks = sarama.WaitForAll
    config.Producer.Retry.Max = 5
    config.Producer.Return.Successes = true

    // 连接Kafka
    producer, err := sarama.NewSyncProducer([]string{"localhost:9092"}, config)
    if err != nil {
        return nil, fmt.Errorf("failed to create Kafka producer: %v", err)
    }

    // 创建消费者配置
    consumerConfig := sarama.NewConfig()
    consumerConfig.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
    consumerConfig.Consumer.Offsets.Initial = sarama.OffsetNewest

    // 创建消费者
    consumer, err := sarama.NewConsumer([]string{"localhost:9092"}, consumerConfig)
    if err != nil {
        producer.Close()
        return nil, fmt.Errorf("failed to create Kafka consumer: %v", err)
    }

    //====================GHY======================//
    // 使用连接字符串连接到MySQL数据库
    dsn := "root:As984315#@tcp(42.194.236.54:3306)/ipfs_nodes?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&timeout=30s&readTimeout=30s&writeTimeout=30s"
    db, err := connectWithRetry(dsn, 3)
    if err != nil {
        producer.Close()
        consumer.Close()
        return nil, fmt.Errorf("failed to connect to database: %v", err)
    }

    // 优化数据库连接池参数
    db.SetMaxOpenConns(10)
    db.SetMaxIdleConns(5)
    db.SetConnMaxLifetime(time.Hour)
    //====================GHY======================//

    // 创建NodeConsumer
    nodeConsumer := &NodeConsumer{
        consumer: consumer,
        db:       db,
        topic:    "ipfs-nodes",
        ready:    make(chan bool),
        stopCh:   make(chan struct{}),
    }

    // 创建NodeExporter
    exporter := &NodeExporter{
        kafkaProducer: producer,
        topic:         "ipfs-nodes",
        consumer:      nodeConsumer,
        stopCh:        make(chan struct{}),
    }

    // 启动消费者
    exporter.wg.Add(1)
    go exporter.startConsumer()

    return exporter, nil
}

func (ne *NodeExporter) startConsumer() {
    defer ne.wg.Done()

    // 获取所有分区
    partitions, err := ne.consumer.consumer.Partitions(ne.topic)
    if err != nil {
        fmt.Printf("Failed to get partitions: %v\n", err)
        return
    }

    // 为每个分区创建一个消费者
    for _, partition := range partitions {
        pc, err := ne.consumer.consumer.ConsumePartition(ne.topic, partition, sarama.OffsetNewest)
        if err != nil {
            fmt.Printf("Failed to start consumer for partition %d: %v\n", partition, err)
            continue
        }

        go func(pc sarama.PartitionConsumer) {
            defer pc.Close()

            for {
                select {
                case msg := <-pc.Messages():
                    if err := ne.processMessage(msg); err != nil {
                        fmt.Printf("Error processing message: %v\n", err)
                    }
                case <-ne.stopCh:
                    return
                }
            }
        }(pc)
    }
}

//====================GHY======================//
// 消息处理函数，添加了重试机制和死锁处理
func (ne *NodeExporter) processMessage(msg *sarama.ConsumerMessage) error {
    var nodeInfo NodeInfo
    if err := json.Unmarshal(msg.Value, &nodeInfo); err != nil {
        return fmt.Errorf("failed to unmarshal message: %v", err)
    }

    // 添加重试机制和死锁处理
    maxRetries := 3
    var lastErr error

    for i := 0; i < maxRetries; i++ {
        if err := ne.processMessageWithTransaction(&nodeInfo); err != nil {
            lastErr = err
            if strings.Contains(err.Error(), "Deadlock found") {
                fmt.Printf("Deadlock detected, retrying (%d/%d)...\n", i+1, maxRetries)
                time.Sleep(time.Millisecond * 500 * time.Duration(i+1))
                continue
            }
            return err
        }
        return nil
    }

    return fmt.Errorf("failed to process message after %d retries: %v", maxRetries, lastErr)
}
//====================GHY======================//

//====================GHY======================//
// 事务处理函数，完全重构以适应数据库表结构和解决死锁问题
func (ne *NodeExporter) processMessageWithTransaction(nodeInfo *NodeInfo) error {
    // 开始事务
    tx, err := ne.consumer.db.Begin()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer tx.Rollback()

    // 获取或创建节点记录
    var nodeID int64
    err = tx.QueryRow(`
        SELECT id FROM discovered_nodes 
        WHERE peer_id = ?
    `, nodeInfo.PeerID).Scan(&nodeID)
    
    if err != nil {
        if err == sql.ErrNoRows {
            // 节点不存在，插入新节点
            result, err := tx.Exec(`
                INSERT INTO discovered_nodes 
                (peer_id, first_seen, last_seen, connection_type, protocol_version, agent_version, latency, 
                 total_addresses, is_reachable, last_successful_connection)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, TRUE, ?)
            `,
                nodeInfo.PeerID,
                nodeInfo.FirstSeen,
                nodeInfo.LastSeen,
                nodeInfo.ConnectionType,
                nodeInfo.ProtocolVersion,
                nodeInfo.AgentVersion,
                nodeInfo.Latency,
                len(nodeInfo.Addresses),
                nodeInfo.LastSeen,
            )
            if err != nil {
                return fmt.Errorf("failed to insert node info: %v", err)
            }
            
            // 获取新插入的节点ID
            lastID, err := result.LastInsertId()
            if err != nil {
                return fmt.Errorf("failed to get last insert ID: %v", err)
            }
            nodeID = lastID
        } else {
            return fmt.Errorf("failed to query node: %v", err)
        }
    } else {
        // 节点存在，更新节点信息
        _, err = tx.Exec(`
            UPDATE discovered_nodes SET
            last_seen = ?,
            connection_type = ?,
            protocol_version = ?,
            agent_version = ?,
            latency = ?,
            total_addresses = ?,
            is_reachable = TRUE,
            last_successful_connection = ?
            WHERE id = ?
        `,
            nodeInfo.LastSeen,
            nodeInfo.ConnectionType,
            nodeInfo.ProtocolVersion,
            nodeInfo.AgentVersion,
            nodeInfo.Latency,
            len(nodeInfo.Addresses),
            nodeInfo.LastSeen,
            nodeID,
        )
        if err != nil {
            return fmt.Errorf("failed to update node info: %v", err)
        }
    }

    // 插入新的地址信息
    for _, addr := range nodeInfo.Addresses {
        // 先检查地址是否已存在
        var addressID int64
        err = tx.QueryRow(`
            SELECT id FROM node_addresses 
            WHERE node_id = ? AND protocol = ? AND host = ? AND port = ?
        `, 
            nodeID, 
            addr.Protocol, 
            addr.Host, 
            addr.Port,
        ).Scan(&addressID)
        
        if err != nil {
            if err == sql.ErrNoRows {
                // 地址不存在，插入新地址
                _, err = tx.Exec(`
                    INSERT INTO node_addresses 
                    (node_id, protocol, host, port, transport, transport_version, is_primary, last_seen)
                    VALUES (?, ?, ?, ?, ?, ?, 0, ?)
                `,
                    nodeID,
                    addr.Protocol,
                    addr.Host,
                    addr.Port,
                    addr.Transport,
                    addr.Version,
                    nodeInfo.LastSeen,
                )
                if err != nil {
                    return fmt.Errorf("failed to insert address: %v", err)
                }
            } else {
                return fmt.Errorf("failed to query address: %v", err)
            }
        } else {
            // 地址存在，更新地址信息
            _, err = tx.Exec(`
                UPDATE node_addresses SET
                transport = ?,
                transport_version = ?,
                last_seen = ?
                WHERE id = ?
            `,
                addr.Transport,
                addr.Version,
                nodeInfo.LastSeen,
                addressID,
            )
            if err != nil {
                return fmt.Errorf("failed to update address: %v", err)
            }
        }
    }

    // 提交事务
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }

    return nil
}
//====================GHY======================//

func (ne *NodeExporter) Close() error {
    // 发送停止信号
    close(ne.stopCh)

    // 等待所有goroutine结束
    ne.wg.Wait()

    var errs []error

    // 关闭Kafka生产者
    if ne.kafkaProducer != nil {
        if err := ne.kafkaProducer.Close(); err != nil {
            errs = append(errs, fmt.Errorf("failed to close kafka producer: %v", err))
        }
    }

    // 关闭Kafka消费者
    if ne.consumer != nil {
        if err := ne.consumer.consumer.Close(); err != nil {
            errs = append(errs, fmt.Errorf("failed to close kafka consumer: %v", err))
        }
        if err := ne.consumer.db.Close(); err != nil {
            errs = append(errs, fmt.Errorf("failed to close database connection: %v", err))
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("errors during cleanup: %v", errs)
    }
    return nil
}

//====================GHY======================//
// 导出节点函数，优化了节点信息的创建和发送过程
func (ne *NodeExporter) ExportNode(ctx context.Context, p peer.AddrInfo, connType string, protocolVersion, agentVersion string, latency int64) error {
    if ne == nil {
        return errors.New("NodeExporter is nil")
    }

    // 创建节点信息
    addresses := make([]AddressInfo, 0, len(p.Addrs))
    for _, addr := range p.Addrs {
        addrInfo, err := parseMultiaddr(addr.String())
        if err != nil {
            fmt.Printf("Warning: Failed to parse multiaddr %s: %v\n", addr.String(), err)
            continue
        }
        addresses = append(addresses, addrInfo)
    }

    nodeInfo := NodeInfo{
        PeerID:          p.ID.String(),
        FirstSeen:       time.Now(),
        LastSeen:        time.Now(),
        ConnectionType:  connType,
        ProtocolVersion: protocolVersion,
        AgentVersion:    agentVersion,
        Latency:        latency,
        Addresses:       addresses,
    }

    // 序列化为JSON
    jsonData, err := json.Marshal(nodeInfo)
    if err != nil {
        return fmt.Errorf("failed to marshal node info: %v", err)
    }

    // 发送到Kafka
    msg := &sarama.ProducerMessage{
        Topic: ne.topic,
        Value: sarama.StringEncoder(jsonData),
    }
    _, _, err = ne.kafkaProducer.SendMessage(msg)
    if err != nil {
        return fmt.Errorf("failed to send message to Kafka: %v", err)
    }

    return nil
}
//====================GHY======================//