package libp2p

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p-kad-dht/dual"

	"go.uber.org/fx"

	"github.com/ipfs/kubo/config"
	"github.com/ipfs/kubo/core/node/helpers"
)

// 主动探索的间隔时间
const activeDiscoveryInterval = time.Minute * 10

// 主动从DHT中查找随机节点，以便发现更多节点
func SetupActiveDiscovery() interface{} {
	return func(mctx helpers.MetricsCtx, lc fx.Lifecycle, host host.Host, dht *dual.DHT, disc *discoveryHandler, cfg *config.Config) (bool, error) {
		if dht == nil {
			log.Info("DHT not available, skipping active peer discovery")
			return false, nil
		}

		// 如果导出节点信息功能未启用，则不进行主动发现
		if !cfg.Discovery.ExportPeers.Enabled {
			log.Info("peer discovery export disabled, skipping active peer discovery")
			return false, nil
		}

		ctx := helpers.LifecycleCtx(mctx, lc)

		// 启动后台协程，定期执行主动发现
		go func() {
			// 等待系统启动完成
			time.Sleep(time.Minute)

			ticker := time.NewTicker(activeDiscoveryInterval)
			defer ticker.Stop()

			log.Info("active peer discovery service started")

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					discoverPeers(ctx, host, disc)
				}
			}
		}()

		return true, nil
	}
}

// 执行主动节点发现，简化为只使用 host API 获取和连接节点
func discoverPeers(ctx context.Context, host host.Host, disc *discoveryHandler) {
	log.Info("starting active peer discovery")

	// 获取当前连接的节点数量作为参考
	currentPeers := len(host.Network().Peers())
	log.Infof("currently connected to %d peers", currentPeers)

	// 使用更简单的方法发现节点：通过现有连接扩散
	// 记录现有的节点，触发节点导出
	for _, p := range host.Network().Peers() {
		// 获取节点信息
		addrInfo := host.Peerstore().PeerInfo(p)
		
		// 触发节点处理和导出
		disc.HandlePeerFound(addrInfo)
		
		// 只处理有限数量的节点
		if len(addrInfo.Addrs) > 0 && currentPeers < 20 {
			// 尝试连接到这个节点，可能会导致发现更多节点
			discCtx, cancel := context.WithTimeout(ctx, time.Second*30)
			if err := host.Connect(discCtx, addrInfo); err != nil {
				log.Debugf("error reconnecting to peer %s: %s", p, err)
			}
			cancel()
		}
	}

	// 从节点仓库获取所有已知节点
	allPeers := host.Peerstore().Peers()
	log.Infof("peerstore contains %d peers", len(allPeers))

	// 尝试连接到一些未连接的节点
	connectedCount := 0
	for _, pid := range allPeers {
		if host.Network().Connectedness(pid) != 2 { // 2表示Connected
			addrInfo := host.Peerstore().PeerInfo(pid)
			if len(addrInfo.Addrs) > 0 {
				discCtx, cancel := context.WithTimeout(ctx, time.Second*15)
				if err := host.Connect(discCtx, addrInfo); err != nil {
					log.Debugf("failed to connect to known peer %s: %s", pid, err)
				} else {
					connectedCount++
					disc.HandlePeerFound(addrInfo)
				}
				cancel()
				
				// 限制一次尝试连接的节点数量
				if connectedCount >= 10 {
					break
				}
			}
		}
	}

	log.Infof("active peer discovery completed, now connected to %d peers", len(host.Network().Peers()))
} 