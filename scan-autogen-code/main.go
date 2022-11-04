package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/chnsz/scan-autogen-code/model"
	"github.com/jmespath/go-jmespath"
	"gopkg.in/yaml.v3"
)

var inputDir string
var outputDir string
var version string
var providerSchemaPath string
var provider string

func init() {
	flag.StringVar(&inputDir, "inputDir", "./input", "The input dir of auto-gen resource yaml")
	flag.StringVar(&outputDir, "outputDir", "./output", "api yaml file output Dir")
	flag.StringVar(&version, "version", "", "provider version")
	flag.StringVar(&providerSchemaPath, "providerSchemaPath", "../schema.json",
		"CMD: terraform providers schema -json >./schema.json")
	flag.StringVar(&provider, "provider", "huaweicloud", "过滤指定provider输出")

}

func main() {
	flag.Parse()
	// 解析 schema, 获取所有的resource和data source列表
	rsNames, dsNames, err := parseSchemaInfo(providerSchemaPath, provider)
	if err != nil {
		fmt.Printf("Failed to parse %s schema file: %s\n", provider, err)
		os.Exit(-1)
	}

	files := GetAllFiles(inputDir, ".yaml")
	for _, v := range files {
		convert(v, outputDir, rsNames, dsNames)
	}
}

// 获取指定目录下的所有文件,包含子目录下的文件
func GetAllFiles(dirPth string, suffix string) (files []string) {
	dir, err := os.ReadDir(dirPth)
	if err != nil {
		log.Printf("error: %v", err)
	}

	sep := string(os.PathSeparator)
	for _, entry := range dir {
		if entry.IsDir() {
			files = append(files, GetAllFiles(dirPth+sep+entry.Name(), suffix)...)
		} else {
			ok := strings.HasSuffix(entry.Name(), suffix)
			if ok {
				files = append(files, dirPth+sep+entry.Name())
			}
		}
	}
	return files
}

func convert(filePath string, outputDir string, rsNames, dsNames []string) {
	inputContent, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Printf("error: %v", err)
	}

	index := strings.LastIndex(filePath, "/")
	if index > 0 {
		index = index + 1
	}
	fileName := filePath[index:]
	resourceName := strings.TrimSuffix(fileName, ".yaml")
	log.Printf("[DEBUG] parsing %s ...", resourceName)
	if _, ok := isExportResource(resourceName, provider, rsNames, dsNames); !ok {
		return
	}

	var inputApi model.Api
	err = yaml.Unmarshal(inputContent, &inputApi)
	if err != nil {
		log.Printf("error: %v", err)
	}

	outputApi := convertApi(inputApi, resourceName)
	outputContent, err := yaml.Marshal(outputApi)
	if err != nil {
		log.Printf("error: %v", err)
	}

	sep := string(os.PathSeparator)
	err = ioutil.WriteFile(outputDir+sep+fileName, outputContent, os.ModePerm)
	if err != nil {
		log.Printf("error: %v", err)
	}
}

func convertApi(inputApi model.Api, resourceName string) model.Api {
	var outputApi model.Api
	outputApi.Info = inputApi.Info
	outputApi.Info.Title = resourceName
	outputApi.Info.Version = version
	outputApi.Servers = inputApi.Servers
	outputApi.Host = "myhuaweicloud.com"
	outputApi.Paths = convertPath(inputApi.Paths)
	outputApi.Tags = parseTags(inputApi.Paths)
	return outputApi
}

func convertPath(paths map[string]map[string]model.OperationInfo) map[string]map[string]model.OperationInfo {
	rst := make(map[string]map[string]model.OperationInfo)
	for _, path := range paths {
		for _, operation := range path {
			apiUrl := strings.Split(operation.XrefApi, " ")
			if len(apiUrl) != 2 {
				log.Println("error bad x-ref-api 格式不正确.", operation.OperationId, operation.XrefApi)
				continue
			}

			if _, ok := rst[apiUrl[1]]; !ok {
				v := make(map[string]model.OperationInfo)
				v[apiUrl[0]] = model.OperationInfo{
					Tag:         operation.XrefProduct,
					OperationId: operation.OperationId,
					XrefApi:     operation.XrefApi,
				}
				rst[apiUrl[1]] = v
			} else {
				rst[apiUrl[1]][apiUrl[0]] = model.OperationInfo{
					Tag:         operation.XrefProduct,
					OperationId: operation.OperationId,
					XrefApi:     operation.XrefApi,
				}
			}

		}
	}
	return rst
}

// 得到所有x-ref-product并去重
func parseTags(paths map[string]map[string]model.OperationInfo) []model.Tag {
	tagSet := make(map[string]string)
	for _, path := range paths {
		for _, operation := range path {
			tagSet[operation.XrefProduct] = "1"
		}
	}
	rst := make([]model.Tag, 0, len(tagSet))
	for k := range tagSet {
		rst = append(rst, model.Tag{Name: k})
	}
	return rst
}

func parseSchemaInfo(schemaJsonPath, provider string) (rsNames []string, dsNames []string, err error) {
	input, err := ioutil.ReadFile(schemaJsonPath)
	if err != nil {
		fmt.Println(err)
		return
	}

	var mapResult map[string]interface{}

	if err = json.Unmarshal(input, &mapResult); err != nil {
		fmt.Println(err)
		return
	}

	sc := mapResult["provider_schemas"].(map[string]interface{})
	for k, v := range sc {
		if strings.Contains(k, provider) {
			m := v.(map[string]interface{})
			rs := m["resource_schemas"].(map[string]interface{})
			ds := m["data_source_schemas"].(map[string]interface{})

			for name, schema := range rs {
				if isDeprecatedResource(schema) {
					continue
				}
				rsNames = append(rsNames, name)
			}
			for name, schema := range ds {
				if isDeprecatedResource(schema) {
					continue
				}
				dsNames = append(dsNames, name)
			}
		}

	}

	return
}

func isDeprecatedResource(schema interface{}) bool {
	v, err := jmespath.Search("block.deprecated", schema)
	if err != nil || v == nil {
		return false
	}
	return true
}

func isExportResource(resourceFileName, provider string, rsNames []string, dsNames []string) (string, bool) {
	if strings.HasPrefix(resourceFileName, "resource_") {
		if len(rsNames) < 1 {
			return "", false
		}
		resourceFileName = strings.Replace(resourceFileName, "huaweicloud", provider, -1)
		simpleFilename := strings.TrimPrefix(resourceFileName, "resource_")
		for _, v := range rsNames {
			if v == simpleFilename {
				return resourceFileName, true
			}
		}
	}

	if strings.HasPrefix(resourceFileName, "data_source_") {
		if len(dsNames) < 1 {
			return "", false
		}
		resourceFileName = strings.Replace(resourceFileName, "huaweicloud", provider, -1)
		simpleFilename := strings.TrimPrefix(resourceFileName, "data_source_")
		for _, v := range dsNames {
			if v == simpleFilename {
				return resourceFileName, true
			}
		}
	}
	return "", false
}
