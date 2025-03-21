package libp2p

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/multiformats/go-multiaddr"

	"go.uber.org/fx"

	"github.com/ipfs/kubo/config"
	"github.com/ipfs/kubo/core/node/helpers"
)

const discoveryConnTimeout = time.Second * 30
const defaultDumpInterval = time.Second * 120    // 默认每2分钟将发现的节点信息写入文件一次

type discoveryHandler struct {
	ctx         context.Context
	host        host.Host
	exporter    *NodeExporter
	exportMux   sync.Mutex
	dumpFile    string
	knownPeers  map[peer.ID]peer.AddrInfo
	peersMutex  sync.RWMutex
	dumpEnabled bool
	dumpInterval time.Duration
}

func NewDiscoveryHandler(ctx context.Context, h host.Host) (*discoveryHandler, error) {
	exporter, err := NewNodeExporter()
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %v", err)
	}

	return &discoveryHandler{
		ctx:        ctx,
		host:       h,
		exporter:   exporter,
		knownPeers: make(map[peer.ID]peer.AddrInfo),
	}, nil
}

func (dh *discoveryHandler) HandlePeerFound(p peer.AddrInfo) {
	dh.exportMux.Lock()
	defer dh.exportMux.Unlock()

	// 获取节点信息
	protocolVersion, _ := dh.host.Peerstore().Get(p.ID, "ProtocolVersion")
	protoVer, _ := protocolVersion.(string)
	
	agentVersion, _ := dh.host.Peerstore().Get(p.ID, "AgentVersion")
	agentVer, _ := agentVersion.(string)
	
	latency := dh.host.Peerstore().LatencyEWMA(p.ID)

	// 导出被动发现的节点信息
	if err := dh.exporter.ExportNode(dh.ctx, p, "passive", protoVer, agentVer, int64(latency)); err != nil {
		log.Errorf("Failed to export passively discovered node info: %v", err)
	}

	// 尝试连接到发现的节点
	if err := dh.host.Connect(dh.ctx, p); err != nil {
		log.Debugf("Failed to connect to peer %s: %s", p.ID, err)
		return
	}

	log.Debugf("Successfully connected to peer %s", p.ID)

	// 记录发现的节点
	if dh.dumpEnabled {
		dh.peersMutex.Lock()
		dh.knownPeers[p.ID] = p
		dh.peersMutex.Unlock()
	}
}

// 将发现的节点信息写入文件
func (dh *discoveryHandler) dumpPeers() {
	if !dh.dumpEnabled {
		return
	}

	for {
		select {
		case <-dh.ctx.Done():
			return
		case <-time.After(dh.dumpInterval):
			dh.writePeersToDumpFile()
		}
	}
}

func (dh *discoveryHandler) writePeersToDumpFile() {
	dh.peersMutex.RLock()
	peers := make([]peer.AddrInfo, 0, len(dh.knownPeers))
	for _, p := range dh.knownPeers {
		peers = append(peers, p)
	}
	dh.peersMutex.RUnlock()

	// 创建目录（如果不存在）
	dir := filepath.Dir(dh.dumpFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Errorf("failed to create directory for dump file: %s", err)
		return
	}

	// 将节点信息序列化为 JSON
	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		log.Errorf("failed to marshal peers data: %s", err)
		return
	}

	// 写入文件
	if err := os.WriteFile(dh.dumpFile, data, 0644); err != nil {
		log.Errorf("failed to write peers to dump file: %s", err)
		return
	}

	log.Infof("wrote %d discovered peers to %s", len(peers), dh.dumpFile)
}

func DiscoveryHandler(mctx helpers.MetricsCtx, lc fx.Lifecycle, host host.Host, cfg *config.Config) *discoveryHandler {
	// 默认保存在 kubo 文件夹下
	dumpFile := "peers.dump"
	
	// 使用配置中的文件路径或使用默认路径
	if cfg.Discovery.ExportPeers.DumpFile != "" {
		dumpFile = cfg.Discovery.ExportPeers.DumpFile
	} else {
		// 尝试使用当前工作目录
		pwd, err := os.Getwd()
		if err == nil {
			// 检查是否在 kubo 目录下，如果不是，尝试找到 kubo 目录
			base := filepath.Base(pwd)
			if base == "kubo" {
				dumpFile = filepath.Join(pwd, "peers.dump")
			} else {
				// 检查 ../kubo 是否存在
				kuboPath := filepath.Join(pwd, "..", "kubo")
				if _, err := os.Stat(kuboPath); err == nil {
					dumpFile = filepath.Join(kuboPath, "peers.dump")
				} else {
					// 尝试找到 IPFS_PATH 环境变量指定的目录
					repoPath, _ := os.LookupEnv("IPFS_PATH")
					if repoPath == "" {
						repoPath = "~/.ipfs"
					}
					dumpFile = filepath.Join(pwd, "peers.dump") // 默认使用当前目录
				}
			}
		}
	}
	
	// 设置导出间隔时间
	dumpInterval := defaultDumpInterval
	if cfg.Discovery.ExportPeers.DumpInterval > 0 {
		dumpInterval = time.Duration(cfg.Discovery.ExportPeers.DumpInterval) * time.Second
	}
	
	dh, err := NewDiscoveryHandler(helpers.LifecycleCtx(mctx, lc), host)
	if err != nil {
		log.Errorf("failed to create discovery handler: %s", err)
		return nil
	}

	dh.dumpFile = dumpFile
	dh.dumpEnabled = cfg.Discovery.ExportPeers.Enabled
	dh.dumpInterval = dumpInterval

	// 启动后台协程，定期将发现的节点信息写入文件
	if dh.dumpEnabled {
		go dh.dumpPeers()
		log.Infof("peer discovery export enabled, writing to %s every %v", dh.dumpFile, dh.dumpInterval)
	}

	return dh
}

func SetupDiscovery(useMdns bool) func(helpers.MetricsCtx, fx.Lifecycle, host.Host, *discoveryHandler) error {
	return func(mctx helpers.MetricsCtx, lc fx.Lifecycle, host host.Host, handler *discoveryHandler) error {
		// 启动MDNS发现（被动发现）
		if useMdns {
			service := mdns.NewMdnsService(host, mdns.ServiceName, handler)
			if err := service.Start(); err != nil {
				log.Error("error starting mdns service: ", err)
				return nil
			}
		}

		// 增加连接事件监听，以捕获所有连接的节点（被动发现）
		host.Network().Notify(&network.NotifyBundle{
			ConnectedF: func(net network.Network, conn network.Conn) {
				if handler.dumpEnabled {
					addrs := []multiaddr.Multiaddr{conn.RemoteMultiaddr()}
					p := peer.AddrInfo{
						ID:    conn.RemotePeer(),
						Addrs: addrs,
					}
					handler.peersMutex.Lock()
					handler.knownPeers[p.ID] = p
					handler.peersMutex.Unlock()

					// 获取节点信息并导出（被动发现）
					protocolVersion, _ := host.Peerstore().Get(p.ID, "ProtocolVersion")
					protoVer, _ := protocolVersion.(string)
					
					agentVersion, _ := host.Peerstore().Get(p.ID, "AgentVersion")
					agentVer, _ := agentVersion.(string)
					
					latency := host.Peerstore().LatencyEWMA(p.ID)
					
					// 导出节点信息
					if err := handler.exporter.ExportNode(mctx, p, "passive", protoVer, agentVer, int64(latency)); err != nil {
						log.Errorf("Failed to export passively discovered node info: %v", err)
					}
				}
			},
		})

		// 启动主动发现服务
		go func() {
			// 等待系统启动完成
			time.Sleep(time.Minute)

			ticker := time.NewTicker(activeDiscoveryInterval)
			defer ticker.Stop()

			log.Info("active peer discovery service started")

			for {
				select {
				case <-mctx.Done():
					return
				case <-ticker.C:
					// 执行主动发现
					for _, p := range host.Network().Peers() {
						addrInfo := host.Peerstore().PeerInfo(p)
						handler.HandlePeerFound(addrInfo)
					}
				}
			}
		}()

		return nil
	}
}

// Close 关闭发现处理程序
func (dh *discoveryHandler) Close() error {
	if dh.exporter != nil {
		return dh.exporter.Close()
	}
	return nil
}
