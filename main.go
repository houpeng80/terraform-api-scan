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
	flag.StringVar(&version, "version", "./api/", "provider version")
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

	// 预处理: 将功能性文件合并至对应的资源文件
	subPackagePath := basePath + provider + "/"
	mergeFunctionFileToInvokeFile(subPackagePath)

	// 处理目录和子目录
	var publicFuncArray []string
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
		log.Println("scan path failed:", err)
	}

	// 将固定的文件替换到指定目录
	copy(outputDir, version, "data_source_huaweicloud_csms_secret_version.yaml")

}

func copy(outputDir, version, src string) error {
	input, err := ioutil.ReadFile("../../config/static/" + src)
	if err != nil {
		fmt.Println(err)
		return err
	}

	input = bytes.Replace(input, []byte("v1.34.1"), []byte(version), 1)

	err = ioutil.WriteFile(outputDir+src, input, 0644)
	if err != nil {
		return err
	}
	return nil
}

// mergeFunctionFileToInvokeFile 将两个文件进行合并
func mergeFunctionFileToInvokeFile(serviceBasePath string) {
	fileMap := map[string]string{
		"compute_instance_v2_networking.go": "resource_huaweicloud_compute_instance.go",
		"compute_interface_attach_v2.go":    "resource_huaweicloud_compute_interface_attach.go",
		"networking_port_v2.go":             "resource_huaweicloud_networking_port_v2.go",
	}

	for k, v := range fileMap {
		set, f, resourceFilebytes := parseFileSrc(serviceBasePath + k)
		set2, f2, resourceFilebytes2 := parseFileSrc(serviceBasePath + v)

		var importsArray []string
		for _, item := range f.Imports {
			importsArray = append(importsArray, item.Path.Value)
		}
		for _, item := range f2.Imports {
			importsArray = append(importsArray, item.Path.Value)
		}
		importsArray = removeDuplicateValues(importsArray)

		var funcArray []string
		for _, d := range f.Decls {
			if fn, isFn := d.(*ast.FuncDecl); isFn {
				startIndex := set.Position(fn.Pos()).Offset
				endIndex := set.Position(fn.End()).Offset

				funcSrc := string(resourceFilebytes[startIndex:endIndex])
				funcArray = append(funcArray, funcSrc)

			}
		}

		endIndex := set2.Position(f2.End()).Offset
		importStartIndex := set.Position(f2.Imports[0].Pos()).Offset
		importEndIndex := set.Position(f2.Imports[len(f2.Imports)-1].End()).Offset

		result := string(resourceFilebytes2[:importStartIndex])
		result = result + "\n" + strings.Join(importsArray, "\n")
		result = result + "\n" + string(resourceFilebytes2[importEndIndex:endIndex])
		result = result + "\n" + strings.Join(funcArray, "\n")

		os.Remove(basePath + k)

		ioutil.WriteFile(serviceBasePath+v, []byte(result), 0664)
	}
}

// unused func
func parsePublicFunction(serviceBasePath string) []string {
	fileMap := []string{
		"compute_instance_v2_networking.go",
		"compute_interface_attach_v2.go",
		"networking_port_v2.go",
	}
	var funcArray []string
	for _, v := range fileMap {
		set, f, resourceFilebytes := parseFileSrc(serviceBasePath + v)

		for _, d := range f.Decls {
			if fn, isFn := d.(*ast.FuncDecl); isFn {
				startIndex := set.Position(fn.Pos()).Offset
				endIndex := set.Position(fn.End()).Offset
				funcSrc := string(resourceFilebytes[startIndex:endIndex])
				funcArray = append(funcArray, funcSrc)

			}
		}
	}
	return funcArray

}

func parseFileSrc(filePath string) (fset *token.FileSet, f *ast.File, resourceFilebytes []byte) {
	set := token.NewFileSet()
	f, err := parser.ParseFile(set, filePath, nil, 0)
	if err != nil {
		log.Println("Failed to parse file:", filePath, err)
		return
	}

	resourceFilebytes, err2 := ioutil.ReadFile(filePath)
	if err2 != nil {
		log.Fatal(err2)
	}
	return set, f, resourceFilebytes
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
				log.Println("skip file which is deprecated or testing:", filePath)
				continue
			}

			// 忽略非resource和data source文件
			if strings.LastIndex(filePath, "resource_huaweicloud_") == -1 &&
				strings.LastIndex(filePath, "data_source_huaweicloud_") == -1 {
				log.Println("skip file which is neither resource nor data source:", filePath)
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
	}

	for _, v := range internalFiles {
		if strings.LastIndex(filePath, v) > -1 {
			return true
		}
	}
	return false
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
	//fmt.Println("tags.length:", len(tags))

	tags = removeDuplicateValues(tags)
	//如果有多个tags,则找到关键资源
	if len(tags) > 1 {
		var mainTag string
		existErrTag := false
		for _, v := range tags {
			if v != "" {
				if strings.Contains(resourceName, fmt.Sprintf("_%s_", strings.ToLower(v))) {
					mainTag = v
				}
			} else {
				existErrTag = true
			}
		}

		if existErrTag {
			log.Println("资源文件存在解析异常,部分方法缺少tag,请查看", resourceName)
		}

		if mainTag != "" {
			tags = []string{mainTag}
		}
	}

	for i, v := range tags {
		tags[i] = fmt.Sprintf("\n  - name: %s", v)
	}

	if waitingUpdateResource(resourceName) {
		description = "404.This resource is waiting to be upgraded, so there is none method output."
		log.Println(description, resourceName)
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
		existErrTag := false
		for _, v := range tags {
			if v != "" {
				if strings.Contains(resourceName, fmt.Sprintf("_%s_", strings.ToLower(v))) {
					mainTag = v
				}
			} else {
				existErrTag = true
			}
		}

		if existErrTag {
			log.Println("资源文件存在解析异常,部分方法缺少tag,请查看", resourceName)
		}

		if mainTag != "" {
			tags = []string{mainTag}
		}
	}

	for i, v := range tags {
		tags[i] = fmt.Sprintf("\n  - name: %s", v)
	}

	if waitingUpdateResource(resourceName) {
		description = "404.This resource is waiting to be upgraded, so there is none method output."
		log.Println(description, resourceName)
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

func fixProduct(resourcesType, curFilePath string) string {
	// 这里将EIP的几个服务的 tags 产品从vpc 改为EIP
	specifyFiles := []string{
		"data_source_huaweicloud_vpc_bandwidth.go",
		"data_source_huaweicloud_vpc_eip.go",
		"data_source_huaweicloud_vpc_eips.go",
		"resource_huaweicloud_eip_associate.go",
		"resource_huaweicloud_vpc_bandwidth.go",
		"resource_huaweicloud_vpc_eip.go",
	}

	if v, ok := isSpecifyName(specifyFiles, "EIP", resourcesType, curFilePath); ok {
		return v
	}

	// Kafka
	specifyFiles = []string{
		"resource_huaweicloud_dms_kafka_instance.go",
		"resource_huaweicloud_dms_kafka_topic.go",
		"data_source_huaweicloud_vpc_eips.go",
		"resource_huaweicloud_eip_associate.go",
		"resource_huaweicloud_vpc_bandwidth.go",
		"resource_huaweicloud_vpc_eip.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "Kafka", resourcesType, curFilePath); ok {
		return v
	}

	// RabbitMQ
	specifyFiles = []string{
		"resource_huaweicloud_dms_rabbitmq_instance.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "RabbitMQ", resourcesType, curFilePath); ok {
		return v
	}
	// lb
	specifyFiles = []string{
		"resource_huaweicloud_lb_loadbalancer.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "ELB", resourcesType, curFilePath); ok {
		return v
	}

	// FunctionGraph
	specifyFiles = []string{
		"resource_huaweicloud_fgs_trigger.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "FunctionGraph", resourcesType, curFilePath); ok {
		return v
	}

	// ecs
	specifyFiles = []string{
		"resource_huaweicloud_compute_interface_attach.go",
		"resource_huaweicloud_compute_instance.go",
		"resource_huaweicloud_compute_eip_associate.go",
		"data_source_huaweicloud_compute_instance.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "ECS", resourcesType, curFilePath); ok {
		return v
	}

	// APIG
	specifyFiles = []string{
		"resource_huaweicloud_apig_vpc_channel.go",
		"resource_huaweicloud_apig_instance.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "APIG", resourcesType, curFilePath); ok {
		return v
	}

	// MRS
	specifyFiles = []string{
		"resource_huaweicloud_mapreduce_cluster.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "MRS", resourcesType, curFilePath); ok {
		return v
	}

	// CCE
	specifyFiles = []string{
		"resource_huaweicloud_cce_node.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "CCE", resourcesType, curFilePath); ok {
		return v
	}

	// nosql
	specifyFiles = []string{
		"resource_huaweicloud_gaussdb_redis_instance.go",
		"resource_huaweicloud_gaussdb_cassandra_instance.go",
		"data_source_huaweicloud_gaussdb_cassandra_dedicated_resource.go",
		"data_source_huaweicloud_gaussdb_cassandra_instance.go",
		"data_source_huaweicloud_gaussdb_cassandra_instances.go",
		"data_source_huaweicloud_gaussdb_cassandra_flavors.go",
		"data_source_huaweicloud_gaussdb_nosql_flavors.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "GaussDBforNoSQL", resourcesType, curFilePath); ok {
		return v
	}

	// openGauss
	specifyFiles = []string{
		"data_source_huaweicloud_gaussdb_opengauss_instance.go",
		"data_source_huaweicloud_gaussdb_opengauss_instances.go",
		"resource_huaweicloud_gaussdb_opengauss_instance.go",
	}
	if v, ok := isSpecifyName(specifyFiles, "GaussDBforopenGauss", resourcesType, curFilePath); ok {
		return v
	}

	return resourcesType

}

func isSpecifyName(files []string, product, orignalName, curFilePath string) (name string, ok bool) {
	for _, v := range files {
		if strings.LastIndex(curFilePath, v) > -1 {
			ok = true
			break
		}
	}

	if ok == true {
		return product, ok
	}

	return orignalName, ok
}

// 未迁移至sdk的资源
func waitingUpdateResource(resourceName string) bool {
	deprecateFiles := []string{
		"data_source_huaweicloud_gaussdb_mysql_flavors",
		"data_source_huaweicloud_obs_bucket_object",
		"resource_huaweicloud_cloudtable_cluster",
		"resource_huaweicloud_obs_bucket_object",
		"resource_huaweicloud_obs_bucket_policy",
		"resource_huaweicloud_obs_bucket",
	}
	for _, v := range deprecateFiles {
		if strings.LastIndex(resourceName, v) > -1 {
			return true
		}
	}
	return false
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

			for name := range rs {
				rsNames = append(rsNames, name)
			}
			for name := range ds {
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

func isExportResource(resourceFileName, provider string, rsNames []string, dsNames []string) (string, bool) {
	re3, _ := regexp.Compile(`^_v\d+$`)

	if strings.HasPrefix(resourceFileName, "resource_") {
		if len(rsNames) < 1 {
			return "", false
		}
		resourceFileName = mappingTo(resourceFileName, provider)
		resourceFileName = strings.Replace(resourceFileName, "huaweicloud", provider, -1)
		simpleFilename := strings.TrimPrefix(resourceFileName, "resource_")
		for _, v := range rsNames {
			remaindStr := strings.TrimPrefix(v, simpleFilename)
			if remaindStr == "" || re3.MatchString(remaindStr) {
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
			if remaindStr == "" || re3.MatchString(remaindStr) {
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
