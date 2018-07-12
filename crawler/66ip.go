package crawler

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/naiba/com"
	"github.com/naiba/proxyinabox"
)

//P66IP 66ip site
type P66IP struct {
	urls        []string
	currentType int
}

//New66IP new 66ip
func New66IP() *P66IP {
	this := new(P66IP)
	this.urls = []string{
		"http://www.66ip.cn/mo.php?tqsl=1000",
		"http://www.66ip.cn/nmtq.php?getnum=1000",
	}
	return this
}

//Get get proxies
func (p *P66IP) Get() error {
	resp, err := http.Get(p.urls[p.currentType])
	if err != nil {
		return err
	}
	body, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	lines := strings.Split(string(body), "<br />")
	for _, line := range lines {
		ipinfo := strings.Split(strings.TrimSpace(line), ":")
		fmt.Println(ipinfo)
		if len(ipinfo) == 2 && com.IsIPv4(ipinfo[0]) {
			var p proxyinabox.Proxy
			p.IP = ipinfo[0]
			p.Port = ipinfo[1]

			validateJobs <- p
		}
	}

	p.currentType++
	if p.currentType == len(p.urls) {
		p.currentType = 0
	}

	return nil
}