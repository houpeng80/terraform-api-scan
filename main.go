package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jmespath/go-jmespath"
)

var (
	// 命令行参数
	basePath           string
	filterFilePath     string
	outputDir          string
	version            string
	providerSchemaPath string
	provider           string

	// 保存 openstack/instances.{func} : uri
	urlSupportsInUriFile     = make(map[string]string)
	urlSupportsInRequestFile = make(map[string]CloudUri)
)

func init() {
	log.SetOutput(os.Stdout)

	flag.StringVar(&basePath, "basePath", "./", "base Path")
	flag.StringVar(&outputDir, "outputDir", "./api/", "api yaml file output Dir")
	flag.StringVar(&version, "version", "", "provider version")
	flag.StringVar(&filterFilePath, "filterFilePath", "", "Specifies the terraform resource been scan")
	flag.StringVar(&providerSchemaPath, "providerSchemaPath", "schema.json",
		"CMD: terraform providers schema -json >./schema.json")
	flag.StringVar(&provider, "provider", "huaweicloud", "过滤指定provider输出")
}
func main() {

	flag.Parse()
	log.Printf("basePath: %s\n", basePath)

	// 解析 config, 获取client和catalog的对应关系
	parseConfigFile(basePath + "huaweicloud/config/config.go")
	parseHCConfigFile(basePath + "huaweicloud/config/hc_config.go")

	// 解析 schema, 获取所有的resource和data source列表
	rsNames, dsNames, err := parseSchemaInfo(providerSchemaPath, provider)
	if err != nil {
		fmt.Printf("Failed to parse %s schema file: %s\n", provider, err)
		os.Exit(-1)
	}

	// 处理目录和子目录
	var publicFuncArray []string
	subPackagePath := basePath + provider + "/"
	err = filepath.Walk(subPackagePath, func(path string, fInfo os.FileInfo, err error) error {
		if err != nil {
			log.Printf("scan path %s failed: %s\n", path, err)
			return err
		}

		if fInfo.IsDir() && !isSkipDirectory(path) {
			searchPackage(path, publicFuncArray, rsNames, dsNames, provider)
		}

		return nil
	})

	if err != nil {
		fmt.Printf("ERROR: scan path failed: %s\n", err)
	}

	// 将固定的文件替换到指定目录并替换版本号
	if err := copy(outputDir, version); err != nil {
		fmt.Printf("ERROR: copy static files failed: %s\n", err)
	}

	copyFromFile(outputDir, "resource_huaweicloud_gaussdb_cassandra_instance.yaml", "resource_huaweicloud_gaussdb_mongo_instance.yaml")
	copyFromFile(outputDir, "resource_huaweicloud_gaussdb_cassandra_instance.yaml", "resource_huaweicloud_gaussdb_influx_instance.yaml")
}

func copy(outputDir, version string) error {
	return filepath.Walk("../../config/static/", func(path string, fInfo os.FileInfo, err error) error {
		if err != nil {
			log.Printf("scan path %s failed: %s\n", path, err)
			return err
		}

		// 忽略目录
		if fInfo.IsDir() {
			return nil
		}

		rawBytes, err := ioutil.ReadFile(path)
		if err != nil {
			fmt.Println(err)
			return err
		}

		input := bytes.Replace(rawBytes, []byte("v1.xx.y"), []byte(version), 1)

		fmt.Printf("copy file %s into %s\n", path, outputDir)
		outputFile := outputDir + fInfo.Name()
		return ioutil.WriteFile(outputFile, input, 0644)
	})
}

func copyFromFile(dir, source, target string) error {
	fmt.Printf("copy file %s as %s\n", source, target)
	sourcePath := filepath.Join(dir, source)
	rawBytes, err := ioutil.ReadFile(sourcePath)
	if err != nil {
		fmt.Println(err)
		return err
	}

	sourceName := strings.TrimSuffix(source, ".yaml")
	targetName := strings.TrimSuffix(target, ".yaml")
	input := bytes.Replace(rawBytes, []byte(sourceName), []byte(targetName), 1)

	targetPath := filepath.Join(dir, target)
	return ioutil.WriteFile(targetPath, input, 0644)
}

func searchPackage(subPackage string, publicFuncs, rsNames, dsNames []string, provider string) {
	set := token.NewFileSet()
	packs, err := parser.ParseDir(set, subPackage, nil, 0)
	if err != nil {
		fmt.Printf("Failed to parse package %s: %s\n", subPackage, err)
		os.Exit(1)
	}

	log.Printf("current scan path: %s, package count: %d\n", subPackage, len(packs))

	skipFiles := []string{"\n"}

	for _, pack := range packs {
		packageName := pack.Name

		log.Printf("package name: %s, file count: %d\n", packageName, len(pack.Files))
		for filePath, f := range pack.Files {
			// 忽略指定的路径
			if len(filterFilePath) > 0 && strings.LastIndex(filePath, filterFilePath) > 0 {
				log.Println("skip file which is specified by -filterFilePath:", filePath)
				continue
			}

			// 忽略测试文件和deprecated的资源
			if strings.LastIndex(filePath, "test.go") > 0 || isDeprecatedFile(filePath) ||
				isInternalFile(filePath) {
				log.Println("skip file which is deprecated, internal or testing:", filePath)
				continue
			}

			// 忽略非resource和data source文件
			if strings.LastIndex(filePath, "resource_huaweicloud_") == -1 &&
				strings.LastIndex(filePath, "data_source_huaweicloud_") == -1 {
				log.Println("skip file which is neither resource nor data source:", filePath)
				skipFiles = append(skipFiles, filePath)
				continue
			}

			// 忽略自动生成的文件
			if isAutoGenetatedFile(filePath) {
				log.Println("skip file which is auto generrated", filePath)
				skipFiles = append(skipFiles, filePath)
				continue
			}

			// 获得文件名并去除版本号
			resourceName := filePath[strings.LastIndex(filePath, "/")+1 : len(filePath)-3]
			re, _ := regexp.Compile(`_v\d+$`)
			resourceName = re.ReplaceAllString(resourceName, "")

			// 根据provider提供的资源，过滤资源
			if rsName, ok := isExportResource(resourceName, provider, rsNames, dsNames); ok {
				log.Printf("parse file %s in %s package ...\n", resourceName, packageName)

				// 优先解析golangsdk, 不支持混用的情况
				var yarmStr string
				if withGolangSDK(f) {
					yarmStr = buildYaml(parseResourceFile(resourceName, filePath, f, set, publicFuncs, rsName))
				} else {
					yarmStr = buildYamlWithoutBase(parseResourceFile2(resourceName, filePath, f, set, publicFuncs, rsName))
				}

				// 保存描述文件
				outputFile := outputDir + strings.Replace(rsName, "huaweicloud", provider, -1) + ".yaml"
				err := ioutil.WriteFile(outputFile, []byte(yarmStr), 0664)
				if err == nil {
					log.Println("写入成功", outputFile)
				}

			} else {
				log.Println("skip file which not export:", filePath)
				skipFiles = append(skipFiles, filePath)
				continue
			}
		}
	}

	// 写入跳过的文件
	fSkip, fskipErr := os.OpenFile(outputDir+"skip_files.txt", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if fskipErr != nil {
		return
	}
	_, err = fSkip.Write([]byte(strings.Join(skipFiles, "\n")))
	fSkip.Close()

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

func isDeprecatedFile(filePath string) bool {
	deprecateFiles := []string{
		"data_source_huaweicloud_antiddos_v1",
		"data_source_huaweicloud_compute_availability_zones_v2",
		"data_source_huaweicloud_csbs_backup_policy_v1",
		"data_source_huaweicloud_csbs_backup_v1",
		"data_source_huaweicloud_cts_tracker_v1",
		"data_source_huaweicloud_networking_network_v2",
		"data_source_huaweicloud_networking_subnet_v2",
		"data_source_huaweicloud_vbs_backup_policy_v2",
		"data_source_huaweicloud_vbs_backup_v2",
		"data_source_huaweicloud_vpc_ids",
		"data_source_huaweicloud_vpc_route_ids",
		"data_source_huaweicloud_vpc_route.go",
		"resource_huaweicloud_blockstorage_volume_v2",
		"resource_huaweicloud_compute_floatingip_v2",
		"resource_huaweicloud_compute_floatingip_associate_v2",
		"resource_huaweicloud_compute_secgroup_v2",
		"resource_huaweicloud_csbs_backup_policy_v1",
		"resource_huaweicloud_csbs_backup_v1",
		"resource_huaweicloud_dms_instance_v1",
		"resource_huaweicloud_ecs_instance_v1",
		"resource_huaweicloud_fw_firewall_group_v2",
		"resource_huaweicloud_fw_policy_v2",
		"resource_huaweicloud_fw_rule_v2",
		"resource_huaweicloud_ges_graph_v1",
		"resource_huaweicloud_networking_floatingip_v2",
		"resource_huaweicloud_networking_floatingip_associate_v2",
		"resource_huaweicloud_networking_network_v2",
		"resource_huaweicloud_networking_port_v2",
		"resource_huaweicloud_networking_router_interface_v2",
		"resource_huaweicloud_networking_router_route_v2",
		"resource_huaweicloud_networking_router_v2",
		"resource_huaweicloud_networking_subnet_v2",
		"resource_huaweicloud_vbs_backup_policy_v2",
		"resource_huaweicloud_vbs_backup_v2",
		"resource_huaweicloud_rts_stack_v1",
		"resource_huaweicloud_rts_software_config_v1",
	}
	for _, v := range deprecateFiles {
		if strings.LastIndex(filePath, v) > -1 {
			return true
		}
	}
	return false
}

func isInternalFile(filePath string) bool {
	internalFiles := []string{
		"resource_huaweicloud_apm_aksk",
		"resource_huaweicloud_aom_alarm_policy",
		"resource_huaweicloud_aom_prometheus_instance",
		"resource_huaweicloud_aom_application",
		"resource_huaweicloud_aom_component",
		"resource_huaweicloud_aom_environment",
		"resource_huaweicloud_aom_cmdb_resource_relationships",
		"resource_huaweicloud_lts_access_rule",
		"resource_huaweicloud_lts_dashboard",
		"resource_huaweicloud_lts_struct_template",
		"resource_huaweicloud_elb_log",
		"resource_huaweicloud_cce_partition",
	}

	for _, v := range internalFiles {
		if strings.LastIndex(filePath, v) > -1 {
			return true
		}
	}
	return false
}

func isAutoGenetatedFile(filePath string) bool {
	var offset int = 200

	fileBytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatal(err)
	}

	header := string(fileBytes[:offset])
	return strings.Contains(header, "*** AUTO GENERATED CODE ***")
}

func withGolangSDK(astFile *ast.File) bool {
	sdkFilePreFix := "github.com/chnsz/golangsdk/openstack/"
	for _, d := range astFile.Imports {
		fullPath := strings.Trim(d.Path.Value, `"`)
		if strings.Contains(fullPath, sdkFilePreFix) {
			return true
		}
	}
	return false
}

func buildYaml(resourceName, description string, cloudUri []CloudUri, filePath, newResourceName string) string {
	var tags = []string{}

	var paths string
	for i, item := range cloudUri {
		resourcesType := item.serviceCatalog.Product

		// 从文件名中获取 catalog
		if resourcesType == "" || resourcesType == "unknown" {
			newCatalog, newType := getCatalogFromName(filePath)
			log.Printf("[WARN] file %s maybe belongs to %s catalog\n", filePath, newType)
			resourcesType = newType
			if newCatalog != nil {
				item.serviceCatalog = *newCatalog
			}
		}

		// VPC和EIP共用一个endpoint, 使用URL进行区分
		if resourcesType == "VPC" && hasEIP(item.url) {
			log.Printf("[DEBUG] update product VPC to EIP because the URI is %s", item.url)
			resourcesType = "EIP"
		}

		// 处理特殊情况
		resourcesType = fixProduct(resourcesType, filePath)

		tags = append(tags, resourcesType)

		resourceBase := "/"
		if item.serviceCatalog.Version != "" {
			resourceBase = resourceBase + item.serviceCatalog.Version + "/"
		}

		if !item.serviceCatalog.WithOutProjectID {
			resourceBase = resourceBase + "{project_id}/"
		}

		if item.serviceCatalog.ResourceBase != "" {
			resourceBase = resourceBase + item.serviceCatalog.ResourceBase + "/"
		}

		isSameWithPre := isSameWithPre(cloudUri, i)
		if isSameWithPre {
			var yamlTemplate = fmt.Sprintf(`
    %s:
      tag: %s
      operationId: %s`, item.httpMethod, resourcesType, item.operationId)
			paths = paths + yamlTemplate
		} else {
			var yamlTemplate = fmt.Sprintf(`
  %s:
    %s:
      tag: %s
      operationId: %s`, resourceBase+item.url, item.httpMethod, resourcesType, item.operationId)
			paths = paths + yamlTemplate
		}
	}

	tags = removeDuplicateValues(tags)
	//如果有多个tags,则找到关键资源
	if len(tags) > 1 {
		var mainTag string
		for _, v := range tags {
			if v == "" {
				log.Printf("[WARN] some tags in %s is empty, please check it", resourceName)
				continue
			}

			if strings.Contains(resourceName, fmt.Sprintf("_%s_", strings.ToLower(v))) {
				mainTag = v
				break
			}
		}

		// 特殊处理
		mainProductMap := map[string]string{
			"resource_huaweicloud_compute_eip_associate": "ECS",
			"resource_huaweicloud_vpc_eip_associate":     "EIP",
		}
		if product, ok := mainProductMap[resourceName]; ok {
			log.Printf("[DEBUG] the main tag of %s should be %s", resourceName, product)
			mainTag = product
		}

		if mainTag == "" {
			log.Printf("[WARN] can not find the main tag of %s, try to get it by path", resourceName)
			_, product := getCatalogFromName(filePath)
			mainTag = fixProduct(product, filePath)
		}

		tags = []string{mainTag}
	}

	for i, v := range tags {
		tags[i] = fmt.Sprintf("\n  - name: %s", v)
	}

	var yamlTemplate = fmt.Sprintf(`info:
  version: %s
  title: %s
  description: %s
schemes:
  - https
host: huaweicloud.com
tags:%s
paths:%s
`, version, strings.Replace(newResourceName, "huaweicloud", provider, -1), description, strings.Join(tags, ""), paths)
	return yamlTemplate
}

// 处理使用huaweicloud-sdk-go-v3的情况
func buildYamlWithoutBase(resourceName, description string, cloudUri []CloudUri, filePath, newResourceName string) string {
	var tags = []string{}

	var paths string
	for i, item := range cloudUri {
		resourcesType := item.serviceCatalog.Product

		// 从文件名中获取 catalog
		if resourcesType == "" || resourcesType == "unknown" {
			newCatalog, newType := getCatalogFromName(filePath)
			log.Printf("[WARN] file %s maybe belongs to %s catalog\n", filePath, newType)
			resourcesType = newType
			if newCatalog != nil {
				item.serviceCatalog = *newCatalog
			}
		}

		// VPC和EIP共用一个endpoint, 使用URL进行区分
		if resourcesType == "VPC" && hasEIP(item.url) {
			log.Printf("[DEBUG] update product VPC to EIP because the URI is %s", item.url)
			resourcesType = "EIP"
		}

		// 处理特殊情况
		resourcesType = fixProduct(resourcesType, filePath)

		tags = append(tags, resourcesType)

		isSameWithPre := isSameWithPre(cloudUri, i)
		if isSameWithPre {
			var yamlTemplate = fmt.Sprintf(`
    %s:
      tag: %s
      operationId: %s`, item.httpMethod, resourcesType, item.operationId)
			paths = paths + yamlTemplate
		} else {
			var yamlTemplate = fmt.Sprintf(`
  %s:
    %s:
      tag: %s
      operationId: %s`, item.url, item.httpMethod, resourcesType, item.operationId)
			paths = paths + yamlTemplate
		}
	}

	tags = removeDuplicateValues(tags)
	//如果有多个tags,则找到关键资源
	if len(tags) > 1 {
		var mainTag string
		for _, v := range tags {
			if v == "" {
				log.Printf("[WARN] some tags in %s is empty, please check it", resourceName)
				continue
			}

			if strings.Contains(resourceName, fmt.Sprintf("_%s_", strings.ToLower(v))) {
				mainTag = v
				break
			}
		}

		if mainTag != "" {
			tags = []string{mainTag}
		}
	}

	for i, v := range tags {
		tags[i] = fmt.Sprintf("\n  - name: %s", v)
	}

	var yamlTemplate = fmt.Sprintf(`info:
  version: %s
  title: %s
  description: %s
schemes:
  - https
host: huaweicloud.com
tags:%s
paths:%s
`, version, strings.Replace(newResourceName, "huaweicloud", provider, -1), description, strings.Join(tags, ""), paths)
	return yamlTemplate
}

func hasEIP(uri string) bool {
	return strings.Contains(uri, "/publicips") || strings.Contains(uri, "publicips/") ||
		strings.Contains(uri, "/bandwidths") || strings.Contains(uri, "bandwidths/")
}

var specialResourceTypes = map[string]string{
	"COMPUTE":   "ECS",
	"ANTIDDOS":  "Anti-DDoS",
	"LB":        "ELB",
	"MAPREDUCE": "MRS",
	"KPS":       "DEW",
	"AOS":       "RFS",
}

// if the file name **contains** the key, then return the product name
var specialResourceKeyMap = map[string]string{
	"_dms_kafka_":    "Kafka",
	"_dms_rabbitmq_": "RabbitMQ",
	"_dms_rocketmq_": "RocketMQ",

	"_gaussdb_cassandra_": "GaussDBforNoSQL",
	"_gaussdb_influx_":    "GaussDBforNoSQL",
	"_gaussdb_mongo_":     "GaussDBforNoSQL",
	"_gaussdb_redis_":     "GaussDBforNoSQL",
	"_gaussdb_mysql_":     "GaussDBforMySQL",
	"_gaussdb_opengauss_": "GaussDB",
}

func fixProduct(resourcesType, curFilePath string) string {
	if v, ok := specialResourceTypes[resourcesType]; ok {
		log.Printf("[WARN] update product %s to %s in %s", resourcesType, v, curFilePath)
		return v
	}

	for k, v := range specialResourceKeyMap {
		if strings.Contains(curFilePath, k) {
			log.Printf("[DEBUG] update product %s to %s in %s", resourcesType, v, curFilePath)
			return v
		}
	}

	return resourcesType
}

func isSpecifyName(files []string, newProduct, orignalName, curFilePath string) (name string, ok bool) {
	for _, v := range files {
		if strings.LastIndex(curFilePath, v) > -1 {
			ok = true
			break
		}
	}

	if ok == true {
		return newProduct, ok
	}

	return orignalName, ok
}

func isSameWithPre(cloudUri []CloudUri, curIndex int) bool {
	if curIndex == 0 {
		return false
	}

	if cloudUri[curIndex].url == cloudUri[curIndex-1].url {
		return true
	}

	return false

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

	//特殊情况
	//huaweicloud_vpc_eip_associate       resource_huaweicloud_eip_associate
	//huaweicloud_vpc_route               resource_huaweicloud_vpc_route_table_route
	//huaweicloud_rds_parametergroup_v3   resource_huaweicloud_rds_configuration_v3
	if len(rsNames) > 0 {
		rsNames = append(rsNames, strings.Replace("huaweicloud_eip_associate", "huaweicloud", provider, -1))
		rsNames = append(rsNames, strings.Replace("huaweicloud_vpc_route_table_route", "huaweicloud", provider, -1))
		rsNames = append(rsNames, strings.Replace("huaweicloud_rds_configuration", "huaweicloud", provider, -1))
	}

	ioutil.WriteFile("resource_name.txt", []byte(strings.Join(rsNames, "\n")), 0644)
	ioutil.WriteFile("data_source_name.txt", []byte(strings.Join(dsNames, "\n")), 0644)
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
	re, _ := regexp.Compile(`^_v[1-9]$`)

	if strings.HasPrefix(resourceFileName, "resource_") {
		if len(rsNames) < 1 {
			return "", false
		}
		resourceFileName = mappingTo(resourceFileName, provider)
		resourceFileName = strings.Replace(resourceFileName, "huaweicloud", provider, -1)
		simpleFilename := strings.TrimPrefix(resourceFileName, "resource_")
		for _, v := range rsNames {
			remaindStr := strings.TrimPrefix(v, simpleFilename)
			if remaindStr == "" || re.MatchString(remaindStr) {
				return resourceFileName, true
			}
		}
	}

	if strings.HasPrefix(resourceFileName, "data_source_") {
		if len(dsNames) < 1 {
			return "", false
		}
		resourceFileName = mappingTo(resourceFileName, provider)
		resourceFileName = strings.Replace(resourceFileName, "huaweicloud", provider, -1)
		simpleFilename := strings.TrimPrefix(resourceFileName, "data_source_")
		for _, v := range dsNames {
			remaindStr := strings.TrimPrefix(v, simpleFilename)
			if remaindStr == "" || re.MatchString(remaindStr) {
				return resourceFileName, true
			}
		}
	}
	return "", false
}

func mappingTo(resourceFileName, provider string) string {
	switch provider {
	case "flexibleengine":
		switch resourceFileName {
		case "resource_huaweicloud_bms_instance":
			return "resource_flexibleengine_compute_bms_server"
		case "resource_huaweicloud_mapreduce_cluster":
			return "resource_flexibleengine_mrs_cluster"
		case "resource_huaweicloud_mapreduce_job":
			return "resource_flexibleengine_mrs_job"
		case "resource_huaweicloud_compute_eip_associate":
			return "resource_flexibleengine_networking_floatingip_associate"
		case "resource_huaweicloud_rds_read_replica_instance":
			return "resource_flexibleengine_rds_read_replica"
		case "resource_huaweicloud_obs_bucket":
			return "resource_flexibleengine_s3_bucket"
		case "resource_huaweicloud_obs_bucket_object":
			return "resource_flexibleengine_s3_bucket_object"
		case "resource_huaweicloud_obs_bucket_policy":
			return "resource_flexibleengine_s3_bucket_policy"
		case "data_source_huaweicloud_evs_volumes":
			return "data_source_flexibleengine_blockstorage_volume"
		case "data_source_huaweicloud_cce_nodes":
			return "data_source_flexibleengine_cce_node_ids"
		case "data_source_huaweicloud_bms_flavors":
			return "data_source_flexibleengine_compute_bms_flavors"
		case "data_source_huaweicloud_dds_flavors":
			return "data_source_flexibleengine_dds_flavor"
		case "data_source_huaweicloud_obs_bucket_object":
			return "data_source_flexibleengine_s3_bucket_object"
		default:
			return resourceFileName
		}
	default:
		return resourceFileName
	}
}
