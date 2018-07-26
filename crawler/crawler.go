package crawler

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/naiba/com"
	"github.com/naiba/proxyinabox"
	"github.com/parnurzeal/gorequest"
)

var validateJobs chan proxyinabox.Proxy
var pendingValidate sync.Map

type validateJSON struct {
	IP       string
	Location struct {
		City        string
		CountryCode string `json:"country_code"`
		CountryName string `json:"country_name"`
		Latitude    string
		Longitude   string
		Province    string
	}
}

func initC() {
	validateJobs = make(chan proxyinabox.Proxy, proxyinabox.Config.Sys.ProxyVerifyWorker*2)
	//start worker
	for i := 1; i <= proxyinabox.Config.Sys.ProxyVerifyWorker; i++ {
		go validator(i, validateJobs)
	}
}

func getDocFromURL(url string) (*goquery.Document, error) {

	_, body, errs := gorequest.New().Get(url).
		Set("User-Agent", com.RandomUserAgent()).
		Retry(3, time.Second*3).
		End()
	if len(errs) > 0 {
		return nil, errs[0]
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	return doc, nil

}

//FetchProxies fetch new proxies
func FetchProxies() {
	cs := []proxyinabox.ProxyCrawler{
		newKuai(),
		newXici(),
		new66IP(),
	}

	for _, c := range cs {
		go c.Fetch()
	}
}

func validator(id int, validateJobs chan proxyinabox.Proxy) {
	for p := range validateJobs {
		// format
		p.IP = strings.TrimSpace(p.IP)
		proxy := p.URI()
		// is processing
		_, has := pendingValidate.Load(proxy)
		if !has && !proxyinabox.CI.HasProxy(p.URI()) {
			pendingValidate.Store(proxy, nil)
			var resp validateJSON
			start := time.Now().Unix()
			_, _, errs := gorequest.New().Timeout(time.Second*7).Retry(3, time.Second*2, http.StatusInternalServerError).Proxy(proxy).Get("http://api.ip.la/cn?json").EndStruct(&resp)
			if len(errs) == 0 && resp.IP == p.IP {
				p.Country = resp.Location.CountryName
				p.Provence = resp.Location.Province
				p.Delay = time.Now().Unix() - start
				p.LastVerify = time.Now()

				if e := proxyinabox.CI.SaveProxy(p); e == nil {
					if proxyinabox.Config.Debug {
						fmt.Println("[PIAB]", "crawler", "[✅]", id, "find a available proxy", p)
					}
				} else {
					fmt.Println("[PIAB]", "crawler", "[❎]", id, "error save proxy", e.Error())
				}
			}
			pendingValidate.Delete(proxy)
		}
	}
}
