package utils

import (
	"sync"

	"github.com/xiaoqidun/qqwry"
)

var (
	ipDBLoaded bool
	ipDBMutex  sync.Mutex
)

// IPInfo 存储查询结果
type IPInfo struct {
	Country string // 国家
	Region  string // 省/州
	City    string // 城市
	ISP     string // 运营商
}

// QueryIP 查询 IP 的国家、省/州、城市、运营商
func QueryIP(ipStr string) (*IPInfo, error) {
	// 确保IP数据库只加载一次
	ipDBMutex.Lock()
	if !ipDBLoaded {
		if err := qqwry.LoadFile("public/qqwry.ipdb"); err != nil {
			ipDBMutex.Unlock()
			return nil, err
		}
		ipDBLoaded = true
	}
	ipDBMutex.Unlock()
	
	location, err := qqwry.QueryIP(ipStr)
	if err != nil {
		return nil, err
	}
	// fmt.Printf("国家：%s，省份：%s，城市：%s，区县：%s，运营商：%s\n",
	// 	location.Country,
	// 	location.Province,
	// 	location.City,
	// 	location.District,
	// 	location.ISP,
	// )
	info := &IPInfo{
		Country: location.Country,
		Region:  location.Province,
		City:    location.City,
		ISP:     location.ISP,
	}
	return info, nil
}
