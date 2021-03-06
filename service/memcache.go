package service

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/naiba/proxyinabox"
)

/*
==============
    代理池
==============
*/
type proxyEntry struct {
	p *proxyinabox.Proxy
	n int64
}

type sortableProxyList []*proxyEntry

func (p sortableProxyList) Len() int {
	return len(p)
}

func (p sortableProxyList) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func (p sortableProxyList) Less(i, j int) bool {
	return p[i].n < p[j].n
}

type proxyList struct {
	l     sync.Mutex
	pl    []*proxyEntry
	index map[string]struct{}
}

/*
==============
   域名IP池
==============
*/
type domainScheduling struct {
	l  sync.Mutex
	dl map[string][]*proxyEntry
}

/*
==============
   IP限流池
==============
*/
type ipActivityEntry struct {
	lastActive int64
	num        int64
}
type ipActivity struct {
	l    sync.Mutex
	list map[string]*ipActivityEntry
}

/*
==============
   域名限流池
==============
*/
type domainActivity struct {
	domains map[string]int64
	last    int64
}
type domainActivityList struct {
	l    sync.Mutex
	list map[string]*domainActivity
}

//MemCache memory cache
type MemCache struct {
	proxies     *proxyList
	domains     *domainScheduling
	ips         *ipActivity
	domainLimit *domainActivityList
}

//NewMemCache rt
func NewMemCache() *MemCache {
	this := &MemCache{
		proxies: &proxyList{
			pl:    make([]*proxyEntry, 0),
			index: make(map[string]struct{}),
		},
		domains: &domainScheduling{
			dl: make(map[string][]*proxyEntry),
		},
		ips: &ipActivity{
			list: make(map[string]*ipActivityEntry),
		},
		domainLimit: &domainActivityList{
			list: make(map[string]*domainActivity),
		},
	}
	this.load()
	this.gc(time.Minute * 10)
	return this
}

func (c *MemCache) load() {
	var ps []proxyinabox.Proxy
	err := proxyinabox.DB.Find(&ps).Error
	if err != nil {
		panic(err)
	}
	c.proxies.l.Lock()
	defer c.proxies.l.Unlock()
	for i := 0; i < len(ps); i++ {
		c.proxies.pl = append(c.proxies.pl, &proxyEntry{
			p: &ps[i],
		})

		c.proxies.index[ps[i].URI()] = struct{}{}
	}
	fmt.Println("[PIAB]", "cache", "[✅]", "load", len(ps), "items!")
}

func (c *MemCache) gc(dur time.Duration) {
	ticker := time.NewTicker(dur)
	go func() {
		for range ticker.C {
			num := 0
			now := time.Now().Unix()
			// 回收域名计数
			c.domainLimit.l.Lock()
			for k, v := range c.domainLimit.list {
				if now-v.last > 60*30 {
					delete(c.domainLimit.list, k)
					num++
				} else {
					for k1, v1 := range v.domains {
						if now-v1 > 60*30 {
							delete(v.domains, k1)
							num++
						}
					}
					if len(v.domains) == 0 {
						delete(c.domainLimit.list, k)
						num++
					}
				}
			}
			c.domainLimit.l.Unlock()
			// 回收IP计数
			now = time.Now().Unix()
			c.ips.l.Lock()
			for k, v := range c.ips.list {
				if v.lastActive != now {
					delete(c.ips.list, k)
					num++
				}
			}
			c.ips.l.Unlock()
			// 回收代理调度
			now = time.Now().Unix()
			c.domains.l.Lock()
			for k, v := range c.domains.dl {
				for i, v1 := range v {
					if now-v1.n > 3 {
						deleteProxyEntrySliceItem(v, i)
					}
				}
				if len(v) == 0 {
					delete(c.domains.dl, k)
					num++
				}
			}
			c.domains.l.Unlock()
			fmt.Println("[PIAB]", "cache GC", "[🚮]", "clean", num, "items.")
		}
	}()
}

//PickProxy rt
func (c *MemCache) PickProxy(req *http.Request) (string, error) {
	c.proxies.l.Lock()
	defer c.proxies.l.Unlock()

	length := len(c.proxies.pl)
	domain := req.Host
	now := time.Now().Unix()
	var candidate map[string]struct{}
	if length == 0 {
		return "", fmt.Errorf("%s", "There is no proxy in the proxy pool.")
	}

	candidate = make(map[string]struct{})
	sort.Sort(sortableProxyList(c.proxies.pl))
	c.domains.l.Lock()
	defer c.domains.l.Unlock()
	if pl, has := c.domains.dl[domain]; has {
		sort.Sort(sortableProxyList(pl))

		//清理长久未活动的代理
		for i, p := range pl {
			if now-p.n < 3 {
				candidate[p.p.IP] = struct{}{}
			} else {
				deleteProxyEntrySliceItem(pl, i)
			}
		}
	} else {
		c.domains.dl[domain] = make([]*proxyEntry, 0)
	}
	for i := 0; i < length; i++ {
		// 检出 3s 内未使用的代理
		if _, has := candidate[c.proxies.pl[i].p.IP]; !has {

			//记录到域名代理表
			c.domains.dl[domain] = append(c.domains.dl[domain], &proxyEntry{
				p: c.proxies.pl[i].p,
				n: now,
			})
			//代理使用次数+1
			c.proxies.pl[i].n++

			fmt.Println("[PIAB]", "proxy scheduling", "[✅]", req.Host, "-->", c.proxies.pl[i].p.URI())

			return c.proxies.pl[i].p.URI(), nil
		}
	}

	return "", fmt.Errorf("%s:all(%d),domain(%s)", "No free agent can be used:", length, domain)
}

//IPLimiter rt
func (c *MemCache) IPLimiter(req *http.Request) bool {
	c.ips.l.Lock()
	defer c.ips.l.Unlock()
	now := time.Now().Unix()
	ip := getIP(req.RemoteAddr)

	entry, has := c.ips.list[ip]
	if has {
		if now == entry.lastActive {
			if entry.num > proxyinabox.Config.Sys.RequestLimitPerIP {
				return false
			}
			entry.num++
		} else {
			entry.num = 1
		}
		entry.lastActive = now
	} else {
		c.ips.list[ip] = &ipActivityEntry{num: 1, lastActive: now}
	}
	return true
}

//HostLimiter rt
func (c *MemCache) HostLimiter(req *http.Request) bool {
	c.domainLimit.l.Lock()
	defer c.domainLimit.l.Unlock()
	ip := getIP(req.RemoteAddr)
	domain := req.Host
	now := time.Now().Unix()
	ds, has := c.domainLimit.list[ip]
	if !has {
		c.domainLimit.list[ip] = &domainActivity{
			domains: make(map[string]int64),
		}
		c.domainLimit.list[ip].domains[domain] = now
		return true
	}
	if now-ds.last > 60*30 {
		ds.domains = make(map[string]int64)
		ds.domains[domain] = now
		ds.last = now
		return true
	}
	ds.domains[domain] = now
	ds.last = now
	for k, v := range ds.domains {
		if now-v > 60*30 {
			delete(ds.domains, k)
		}
	}
	return len(ds.domains) < proxyinabox.Config.Sys.DomainsPerIP
}

//HasProxy rt
func (c *MemCache) HasProxy(proxy string) bool {
	_, has := c.proxies.index[proxy]
	return has
}

//SaveProxy rt
func (c *MemCache) SaveProxy(p proxyinabox.Proxy) error {
	c.proxies.l.Lock()
	defer c.proxies.l.Unlock()
	if e := proxyinabox.DB.Save(&p).Error; e != nil {
		return e
	}
	c.proxies.pl = append(c.proxies.pl, &proxyEntry{
		p: &p,
		n: 0,
	})
	return nil
}

//DeleteProxy rt
func (c *MemCache) DeleteProxy(p proxyinabox.Proxy) {
	if p.ID == 0 {
		return
	}
	c.proxies.l.Lock()
	defer c.proxies.l.Unlock()
	for i, e := range c.proxies.pl {
		if e.p.IP == p.IP {
			deleteProxyEntrySliceItem(c.proxies.pl, i)
		}
	}
	//hard delete
	proxyinabox.DB.Unscoped().Delete(&p)
}

func getIP(str string) string {
	return strings.Split(str, ":")[0]
}

func deleteProxyEntrySliceItem(x []*proxyEntry, i int) {
	if i == len(x)-1 {
		x = append(x[:i])
	} else {
		x = append(x[:i], x[i+1:]...)
	}
}
