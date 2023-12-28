package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jmespath/go-jmespath"
)

var (
	// 命令行参数
	basePath           string
	outputDir          string
	version            string
	providerSchemaPath string
	provider           string
)

func init() {
	flag.StringVar(&basePath, "basePath", "../../terraform-provider-huaweicloud/", "base Path")
	flag.StringVar(&outputDir, "outputDir", "../../test/", "api yaml file output Dir")
	flag.StringVar(&version, "version", "", "provider version")
	flag.StringVar(&providerSchemaPath, "providerSchemaPath", "../schema.json",
		"CMD: terraform providers schema -json >./schema.json")
	flag.StringVar(&provider, "provider", "huaweicloud", "过滤指定provider输出")

}

type ApiConfig struct {
	Info    Info                                    `yaml:"info"`
	Schemes []string                                `yaml:"schemes"`
	Host    string                                  `yaml:"host"`
	Tags    []Tag                                   `yaml:"tags"`
	Paths   map[string]map[string]map[string]string `yaml:"paths"`
}

type Info struct {
	Version     string `yaml:"version"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

type Tag struct {
	Name string `yaml:"name"`
}

func main() {
	flag.Parse()
	// 解析 schema, 获取所有的resource和data source列表
	rsNames, dsNames, err := parseSchemaInfo(providerSchemaPath, provider)
	if err != nil {
		fmt.Printf("Failed to parse %s schema file: %s\n", provider, err)
		os.Exit(-1)
	}

	dealFiles(rsNames, dsNames)
}

func dealFiles(rsNames, dsNames []string) {
	subPackagePath := basePath + provider + "/"
	err := filepath.Walk(subPackagePath, func(path string, fInfo os.FileInfo, err error) error {
		if err != nil {
			log.Printf("scan path %s failed: %s\n", path, err)
			return err
		}

		if fInfo.IsDir() && !isSkipDirectory(path) {
			dealFile(path, rsNames, dsNames)
		}

		return nil
	})
	if err != nil {
		fmt.Printf("ERROR: scan path failed: %s\n", err)
	}
}

func dealFile(path string, rsNames, dsNames []string) {
	fSet := token.NewFileSet()
	packs, err := parser.ParseDir(fSet, path, nil, 0)
	if err != nil {
		fmt.Printf("Failed to parse package %s: %s\n", path, err)
		os.Exit(1)
	}
	for _, pack := range packs {
		packageName := pack.Name
		log.Printf("package name: %s, file count: %d\n", packageName, len(pack.Files))
		for filePath, _ := range pack.Files {
			// 获得文件名并去除版本号
			resourceName := filePath[strings.LastIndex(filePath, "/")+1 : len(filePath)-3]
			re, _ := regexp.Compile(`_v\d+$`)
			resourceName = re.ReplaceAllString(resourceName, "")

			if !isExportResource(resourceName, rsNames, dsNames) {
				continue
			}

			resourceFileBytes, err := os.ReadFile(filePath)
			if err != nil {
				log.Fatal(err)
			}
			fileStr := string(resourceFileBytes)

			// url:{method:{tag:resourcetype}}}
			usedApis := make(map[string]map[string]map[string]string, 0)
			// 匹配所有的API注释信息：eg：// API: DMS GET /v2/{project_id}/instances/{instance_id}
			apiReg := regexp.MustCompile(fmt.Sprintf("(// API:)\\s*(.*)"))
			allApiMatch := apiReg.FindAllStringSubmatch(fileStr, -1)
			if len(allApiMatch) == 0 {
				continue
			}
			isBuildYaml := true
			var product string
			for i, apiMatch := range allApiMatch {
				str := strings.TrimSpace(apiMatch[2])
				standardStr := ""
				var resourceType, url, requestMethod string
				for i, s := range str {
					if string(s) != " " {
						if i > 0 && string(str[i-1]) == " " {
							standardStr += " "
						}
						standardStr += string(s)
					}
				}
				parts := strings.Split(standardStr, " ")
				if len(parts) != 3 {
					log.Printf("[WARN] the resource (%s) API comment(%s) is error, so skip.\n", resourceName, str)
					isBuildYaml = false
					continue
				}
				resourceType = parts[0]
				requestMethod = parts[1]
				url = parts[2]
				if i == 0 {
					product = resourceType
				}
				methodMap, ok := usedApis[url]
				if !ok {
					methodMap = make(map[string]map[string]string)
					usedApis[url] = methodMap
				}
				resourceTypeMap, ok := methodMap[requestMethod]
				if !ok {
					resourceTypeMap = make(map[string]string)
					methodMap[requestMethod] = resourceTypeMap
				}
				if _, ok = resourceTypeMap["tag"]; !ok {
					resourceTypeMap["tag"] = resourceType
				} else {
					if resourceTypeMap["tag"] != resourceType {
						//说明相同接口、相同请求方法中出现了不同的资源类型
						log.Printf("[WARN] the resource (%s) has same API(%s) and method for different type, "+
							"so skip.\n", resourceName, str)
						isBuildYaml = false
						break
					}
				}
			}
			if product == "" {
				log.Printf("[WARN] the resource (%s) service not found, so skip.\n", resourceName)
				continue
			}
			if isBuildYaml {
				buildYaml(resourceName, product, usedApis)
			}
		}
	}
}

func buildYaml(resourceName, product string, paths map[string]map[string]map[string]string) {
	cfg := ApiConfig{
		Info:    Info{Title: resourceName, Version: version},
		Schemes: []string{"https"},
		Host:    "huaweicloud.com",
		Tags:    []Tag{{Name: product}},
		Paths:   paths,
	}
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		log.Fatal(err)
	}

	if err = os.WriteFile(fmt.Sprintf("%s/%s.yaml", outputDir, resourceName), data, 0664); err != nil {
		log.Println("[WARN] write error", resourceName)
		return
	}
	log.Println("write success", resourceName)
}

func parseSchemaInfo(schemaJsonPath, provider string) (rsNames []string, dsNames []string, err error) {
	input, err := os.ReadFile(schemaJsonPath)
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

func isExportResource(resourceName string, rsNames, dsNames []string) bool {
	if strings.HasPrefix(resourceName, "resource_") {
		if len(rsNames) < 1 {
			return false
		}
		resourceName = strings.Replace(resourceName, "huaweicloud", provider, -1)
		simpleFilename := strings.TrimPrefix(resourceName, "resource_")
		for _, v := range rsNames {
			if v == simpleFilename {
				return true
			}
		}
	}

	if strings.HasPrefix(resourceName, "data_source_") {
		if len(dsNames) < 1 {
			return false
		}
		resourceName = strings.Replace(resourceName, "huaweicloud", provider, -1)
		simpleFilename := strings.TrimPrefix(resourceName, "data_source_")
		for _, v := range dsNames {
			if v == simpleFilename {
				return true
			}
		}
	}
	return false
}

func isSkipDirectory(path string) bool {
	var skipKeys = []string{
		"acceptance", "utils", "internal", "helper", "deprecated",
	}

	for _, sub := range skipKeys {
		if strings.Contains(path, sub) {
			return true
		}
	}
	return false
}
