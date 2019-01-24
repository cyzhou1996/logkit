package ip

import (
	"errors"
	"strings"
	"sync"

	"github.com/qiniu/logkit/transforms"
	. "github.com/qiniu/logkit/utils/models"
)

const Name = "IP"

const (
	Region       = "Region"
	City         = "City"
	Country      = "Country"
	Isp          = "Isp"
	CountryCode  = "CountryCode"
	Latitude     = "Latitude"
	Longitude    = "Longitude"
	DistrictCode = "DistrictCode"
)

var (
	_ transforms.StatsTransformer = &Transformer{}
	_ transforms.Transformer      = &Transformer{}
	_ transforms.Initializer      = &Transformer{}
	_ transforms.ServerTansformer = &Transformer{}
)

type Transformer struct {
	StageTime   string `json:"stage"`
	Key         string `json:"key"`
	DataPath    string `json:"data_path"`
	TransformAt string `json:"transform_at"`
	KeyAsPrefix bool   `json:"key_as_prefix"`
	Language    string `json:"language"`

	loc   Locator
	stats StatsInfo

	//为了提升性能提前做处理
	keys             []string
	lastEleKey       string
	keysRegion       []string
	keysCity         []string
	keysCountry      []string
	keysIsp          []string
	keysCountryCode  []string
	keysLatitude     []string
	keysLongitude    []string
	keysDistrictCode []string

	numRoutine int
}

func (t *Transformer) Init() error {
	if t.TransformAt == "" || t.TransformAt == "本地" {
		t.TransformAt = Local
	}
	if t.TransformAt == "服务端" {
		t.TransformAt = Server
	}
	if t.TransformAt != Local {
		return nil
	}

	if t.Language == "" {
		t.Language = "zh-CN"
	}
	loc, err := NewLocator(t.DataPath, t.Language)
	if err != nil {
		return err
	}
	t.loc = loc
	t.keys = GetKeys(t.Key)

	newKeys := make([]string, len(t.keys))
	copy(newKeys, t.keys)
	t.lastEleKey = t.keys[len(t.keys)-1]
	t.keysRegion = generateKeys(t.keys, Region, t.KeyAsPrefix)
	t.keysCity = generateKeys(t.keys, City, t.KeyAsPrefix)
	t.keysCountry = generateKeys(t.keys, Country, t.KeyAsPrefix)
	t.keysIsp = generateKeys(t.keys, Isp, t.KeyAsPrefix)
	t.keysCountryCode = generateKeys(t.keys, CountryCode, t.KeyAsPrefix)
	t.keysLatitude = generateKeys(t.keys, Latitude, t.KeyAsPrefix)
	t.keysLongitude = generateKeys(t.keys, Longitude, t.KeyAsPrefix)
	t.keysDistrictCode = generateKeys(t.keys, DistrictCode, t.KeyAsPrefix)

	numRoutine := MaxProcs
	if numRoutine == 0 {
		numRoutine = 1
	}
	t.numRoutine = numRoutine
	return nil
}

func generateKeys(keys []string, lastEle string, keyAsPrefix bool) []string {
	newKeys := make([]string, len(keys))
	copy(newKeys, keys)
	if keyAsPrefix {
		lastEle = keys[len(keys)-1] + "_" + lastEle
	}
	newKeys[len(keys)-1] = lastEle
	return newKeys
}

func (_ *Transformer) RawTransform(datas []string) ([]string, error) {
	return datas, errors.New("IP transformer not support rawTransform")
}

func (t *Transformer) Transform(datas []Data) ([]Data, error) {
	if t.TransformAt != Local {
		return datas, nil
	}

	if t.loc == nil {
		err := t.Init()
		if err != nil {
			return datas, err
		}
	}

	var (
		err, fmtErr error
		errNum      = 0
		dataLen     = len(datas)

		numRoutine   = t.numRoutine
		dataPipeline = make(chan transforms.TransformInfo)
		resultChan   = make(chan transforms.TransformResult)
		wg           = new(sync.WaitGroup)
	)

	if dataLen < numRoutine {
		numRoutine = dataLen
	}

	for i := 0; i < numRoutine; i++ {
		wg.Add(1)
		go t.transform(dataPipeline, resultChan, wg)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	go func() {
		for idx, data := range datas {
			dataPipeline <- transforms.TransformInfo{
				CurData: data,
				Index:   idx,
			}
		}
		close(dataPipeline)
	}()

	var transformResultSlice = make(transforms.TransformResultSlice, dataLen)
	for resultInfo := range resultChan {
		transformResultSlice[resultInfo.Index] = resultInfo
	}

	for _, transformResult := range transformResultSlice {
		if transformResult.Err != nil {
			err = transformResult.Err
			errNum += transformResult.ErrNum
		}
		datas[transformResult.Index] = transformResult.CurData
	}

	t.stats, fmtErr = transforms.SetStatsInfo(err, t.stats, int64(errNum), int64(dataLen), t.Type())
	return datas, fmtErr
}

//通过层级key设置value值, 如果keys不存在则不加前缀，否则加前缀
func (t *Transformer) SetMapValue(m map[string]interface{}, val interface{}, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	var curr map[string]interface{}
	curr = m
	for _, k := range keys[0 : len(keys)-1] {
		finalVal, ok := curr[k]
		if !ok {
			n := make(map[string]interface{})
			curr[k] = n
			curr = n
			continue
		}
		//判断val是否为map[string]interface{}类型
		if curr, ok = finalVal.(map[string]interface{}); ok {
			continue
		}
		if curr, ok = finalVal.(Data); ok {
			continue
		}
		return errors.New("SetMapValueWithPrefix failed, " + strings.Join(keys, ",") + " is not the type of map[string]interface{}")
	}
	//判断val(k)是否存在
	_, exist := curr[keys[len(keys)-1]]
	if exist {
		curr[t.lastEleKey+"_"+keys[len(keys)-1]] = val
	} else {
		curr[keys[len(keys)-1]] = val
	}
	return nil
}

func (_ *Transformer) Description() string {
	//return "transform ip to country region and isp"
	return "获取IP的区域、国家、城市和运营商信息"
}

func (_ *Transformer) Type() string {
	return "IP"
}

func (_ *Transformer) SampleConfig() string {
	return `{
		"type":"IP",
		"stage":"after_parser",
		"key":"MyIpFieldKey",
		"data_path":"your/path/to/ip.dat"
	}`
}

func (_ *Transformer) ConfigOptions() []Option {
	return []Option{
		{
			KeyName:       transforms.TransformAt,
			Element:       Radio,
			ChooseOnly:    true,
			ChooseOptions: []interface{}{Local, Server},
			Default:       Local,
			Required:      true,
			DefaultNoUse:  false,
			Description:   "运行方式",
			Type:          transforms.TransformTypeString,
			ToolTip:       "本地运行使用客户自己的IP库，更为灵活。服务端运行固定使用七牛IP库，用户无需提供IP库",
		},
		{
			KeyName:      "key",
			ChooseOnly:   false,
			Default:      "",
			Required:     true,
			Placeholder:  "my_field_keyname",
			DefaultNoUse: true,
			Description:  "要进行Transform变化的键(key)",
			ToolTip:      "对该字段的值进行transform变换, 服务端运行不支持嵌套(.)，请先使用rename，本地运行支持",
			Type:         transforms.TransformTypeString,
		},
		{
			KeyName:            "data_path",
			ChooseOnly:         false,
			Default:            "",
			Required:           true,
			Placeholder:        "your/path/to/ip.dat(x)",
			DefaultNoUse:       true,
			Description:        "IP数据库路径(data_path)",
			Type:               transforms.TransformTypeString,
			AdvanceDepend:      transforms.TransformAt,
			AdvanceDependValue: Local,
		},
		{
			KeyName:            "key_as_prefix",
			ChooseOnly:         true,
			ChooseOptions:      []interface{}{false, true},
			Required:           false,
			Default:            true,
			DefaultNoUse:       false,
			Element:            Checkbox,
			Description:        "字段名称作为前缀(key_as_prefix)",
			Type:               transforms.TransformTypeString,
			AdvanceDepend:      transforms.TransformAt,
			AdvanceDependValue: Local,
		},
		{
			KeyName:            "language",
			ChooseOnly:         false,
			Default:            "zh-CN",
			Required:           true,
			Placeholder:        "zh-CN",
			DefaultNoUse:       true,
			Description:        "mmdb格式库使用的语种",
			Advance:            true,
			Type:               transforms.TransformTypeString,
			AdvanceDepend:      transforms.TransformAt,
			AdvanceDependValue: Local,
		},
	}
}

func (t *Transformer) Stage() string {
	return transforms.StageAfterParser
}

func (t *Transformer) Stats() StatsInfo {
	return t.stats
}

func (t *Transformer) SetStats(err string) StatsInfo {
	t.stats.LastError = err
	return t.stats
}

func (t *Transformer) Close() error {
	if t.loc != nil {
		return t.loc.Close()
	}
	return nil
}

func (t *Transformer) ServerConfig() map[string]interface{} {
	config := make(map[string]interface{})
	config[KeyType] = Name
	config[ProcessAt] = t.TransformAt
	config["key"] = t.Key

	return config
}

func init() {
	transforms.Add(Name, func() transforms.Transformer {
		return &Transformer{}
	})
}

func (t *Transformer) transform(dataPipeline <-chan transforms.TransformInfo, resultChan chan transforms.TransformResult, wg *sync.WaitGroup) {
	var (
		err    error
		errNum int
	)
	newKeys := make([]string, len(t.keys))
	for transformInfo := range dataPipeline {
		err = nil
		errNum = 0

		copy(newKeys, t.keys)
		val, getErr := GetMapValue(transformInfo.CurData, t.keys...)
		if getErr != nil {
			errNum, err = transforms.SetError(errNum, getErr, transforms.GetErr, t.Key)
			resultChan <- transforms.TransformResult{
				Index:   transformInfo.Index,
				CurData: transformInfo.CurData,
				ErrNum:  errNum,
				Err:     err,
			}
			continue
		}
		strVal, ok := val.(string)
		if !ok {
			notStringErr := errors.New("transform key " + t.Key + " data type is not string")
			errNum, err = transforms.SetError(errNum, notStringErr, transforms.General, "")
			resultChan <- transforms.TransformResult{
				Index:   transformInfo.Index,
				CurData: transformInfo.CurData,
				ErrNum:  errNum,
				Err:     err,
			}
			continue
		}
		strVal = strings.TrimSpace(strVal)
		info, findErr := t.loc.Find(strVal)
		if findErr != nil {
			errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
			resultChan <- transforms.TransformResult{
				Index:   transformInfo.Index,
				CurData: transformInfo.CurData,
				ErrNum:  errNum,
				Err:     err,
			}
			continue
		}
		findErr = t.SetMapValue(transformInfo.CurData, info.Region, t.keysRegion...)
		if findErr != nil {
			errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
		}
		findErr = t.SetMapValue(transformInfo.CurData, info.City, t.keysCity...)
		if findErr != nil {
			errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
		}
		findErr = t.SetMapValue(transformInfo.CurData, info.Country, t.keysCountry...)
		if findErr != nil {
			errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
		}
		findErr = t.SetMapValue(transformInfo.CurData, info.Isp, t.keysIsp...)
		if findErr != nil {
			errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
		}
		if info.CountryCode != "" {
			findErr = t.SetMapValue(transformInfo.CurData, info.CountryCode, t.keysCountryCode...)
			if findErr != nil {
				errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
			}
		}
		if info.Latitude != "" {
			findErr = t.SetMapValue(transformInfo.CurData, info.Latitude, t.keysLatitude...)
			if findErr != nil {
				errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
			}
		}
		if info.Longitude != "" {
			findErr = t.SetMapValue(transformInfo.CurData, info.Longitude, t.keysLongitude...)
			if findErr != nil {
				errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
			}
		}
		if info.DistrictCode != "" {
			findErr = t.SetMapValue(transformInfo.CurData, info.DistrictCode, t.keysDistrictCode...)
			if findErr != nil {
				errNum, err = transforms.SetError(errNum, findErr, transforms.General, "")
			}
		}

		resultChan <- transforms.TransformResult{
			Index:   transformInfo.Index,
			CurData: transformInfo.CurData,
			ErrNum:  errNum,
			Err:     err,
		}
	}
	wg.Done()
}
