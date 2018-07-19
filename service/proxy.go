package service

import (
	"time"

	"github.com/jinzhu/gorm"
	"github.com/naiba/proxyinabox"
)

//ProxyService mysql proxy service
type ProxyService struct {
	DB *gorm.DB
}

//GetUnVerified get un verified proxies
func (ps *ProxyService) GetUnVerified() (p []proxyinabox.Proxy, e error) {
	e = ps.DB.Select("ip,port,id,last_verify").Where("last_verify < ?", time.Now().Add(time.Minute*time.Duration((proxyinabox.Config.Sys.VerifyDuration-5))*-1)).Find(&p).Error
	return
}
