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
	"sort"
	"strings"
	"testing"

	"github.com/huaweicloud/terraform-provider-huaweicloud/huaweicloud/config"
)

var basePath string

var configFilePath string

var filterFilePath string

var outputDir string

var endPointSrcFilePath string

var version string

var sdkFilePreFix string

var providerSchemaPath string

var provider string

func init() {
	flag.StringVar(&basePath, "basePath", "./", "base Path")
	flag.StringVar(&outputDir, "outputDir", "./api/", "api yaml file output Dir")
	flag.StringVar(&version, "version", "./api/", "provider version")
	flag.StringVar(&filterFilePath, "filterFilePath", "", "Specifies the terraform resource been scan")
	flag.StringVar(&sdkFilePreFix, "sdkFilePreFix", "github.com/chnsz/golangsdk/openstack/",
		"Specifies the path of sdk")
	flag.StringVar(&providerSchemaPath, "providerSchemaPath", "schema.json",
		"CMD: terraform providers schema -json >./schema.json")
	flag.StringVar(&provider, "provider", "huaweicloud", "过滤指定provider输出")
}
func main() {

	flag.Parse()

	configFilePath = basePath + "huaweicloud/config/config.go"

	endPointSrcFilePath = basePath + "huaweicloud/config/endpoints.go"

	log.Printf("basePath:%s", basePath)
	//先解析 config:
	parseConfigFile(configFilePath)
	//获取所有文件
	subPackagePath := basePath + "huaweicloud/"
	mergeFunctionFileToInvokeFile(subPackagePath)

	rsNames, dsNames, err := parseSchemaInfo(providerSchemaPath, provider)

	var publicFuncArray []string
	err = filepath.Walk(subPackagePath, func(path string, fInfo os.FileInfo, err error) error {
		if err != nil {
			log.Println("scan path failed:", err)
			return err
		}

		if fInfo.IsDir() {
			fmt.Println(path, fInfo.Size(), fInfo.Name())

			searchPackage2(path, publicFuncArray, rsNames, dsNames, provider)
		}

		return nil
	})
	if err != nil {
		log.Println("scan path failed:", err)
	}

	//将固定的文件替换到指定目录
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

//将抽取出来的单独方法类写入调用的类中，这里是列举
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
			// startIndex := set.Position(v.Pos()).Offset
			// fmt.Println("这里开始：", string(resourceFilebytes[:startIndex]))
		}
		importsArray = removeDuplicateValues(importsArray)
		fmt.Printf("%v", len(importsArray))
		var funcArray []string
		for _, d := range f.Decls {
			if fn, isFn := d.(*ast.FuncDecl); isFn {
				startIndex := set.Position(fn.Pos()).Offset
				endIndex := set.Position(fn.End()).Offset

				funcSrc := string(resourceFilebytes[startIndex:endIndex])
				funcArray = append(funcArray, funcSrc)

			}
		}

		//ast.Print(set2, f2.Imports[len(f2.Imports)-1])
		fmt.Println(set2.Position(f2.End()).Line)
		endIndex := set2.Position(f2.End()).Offset
		importStartIndex := set.Position(f2.Imports[0].Pos()).Offset
		importEndIndex := set.Position(f2.Imports[len(f2.Imports)-1].End()).Offset

		result := string(resourceFilebytes2[:importStartIndex])
		result = result + "\n" + strings.Join(importsArray, "\n")
		result = result + "\n" + string(resourceFilebytes2[importEndIndex:endIndex])
		result = result + "\n" + strings.Join(funcArray, "\n")

		os.Remove(basePath + k)

		ioutil.WriteFile(serviceBasePath+v, []byte(result), 0664)
		//fmt.Println("这里开始2：", err3)

	}

}

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

func TestMerge(t *testing.T) {
	subPackage := basePath + "huaweicloud/"
	mergeFunctionFileToInvokeFile(subPackage)
	//searchPackage2(subPackage)
}

func TestGenApi(t *testing.T) {
	//先解析 config:
	configFilePath = basePath + "huaweicloud/config/config.go"
	parseConfigFile(configFilePath)
	//获取所有文件

	subPackage := basePath + "huaweicloud/"
	searchPackage2(subPackage, nil, nil, nil, "")
}

func searchPackage2(subPackage string, publicFuncs, rsNames, dsNames []string, provider string) {
	set := token.NewFileSet()
	packs, err := parser.ParseDir(set, subPackage, nil, 0)
	println("current scan path:", subPackage, ",package count:", len(packs))
	if err != nil {
		fmt.Println("Failed to parse package:", err)
		os.Exit(1)
	}

	skipFiles := []string{"\n"}

	for _, pack := range packs {
		fmt.Println("packageName:", pack.Name, ",file_count:", len(pack.Files))
		for filePath, f := range pack.Files {
			//fmt.Println("file path:", filePath)
			if strings.LastIndex(filePath, "test.go") > 0 || isDeprecatedFile(filePath) ||
				strings.LastIndex(filePath, "/deprecated/") > 0 {
				log.Println("skip file:", filePath)
				continue
			}

			if (strings.LastIndex(filePath, "resource_huaweicloud_") > -1 ||
				strings.LastIndex(filePath, "data_source_huaweicloud_") > -1) &&
				strings.LastIndex(filePath, filterFilePath) > -1 {
				packageName := f.Name.Name

				resourceName := filePath[strings.LastIndex(filePath, "/")+1 : len(filePath)-3] //获得文件名字
				//去除版本号
				re3, _ := regexp.Compile(`_v\d+$`)
				resourceName = re3.ReplaceAllString(resourceName, "")

				// 根据provider提供的资源，过滤资源
				if ok := isExportResource(resourceName, provider, rsNames, dsNames); !ok {
					log.Println("skip file which not export:", filePath)
					skipFiles = append(skipFiles, filePath)
					continue
				}
				fmt.Println("file:", resourceName, ":", packageName, ":", f.Package)
				//拿到文件所有信息
				//组装成yaml

				yarmStr := buildYaml(parseResourceFile(resourceName, filePath, f, set, publicFuncs))
				//这里开始一个文件生成一个描述文件

				outputFile := outputDir + strings.Replace(resourceName, "huaweicloud", provider, -1) + ".yaml"
				err := ioutil.WriteFile(outputFile, []byte(yarmStr), 0664)
				if err == nil {
					log.Println("写入成功", outputFile)
				}
			} else {
				log.Println("skip file:", filePath)
				skipFiles = append(skipFiles, filePath)
			}

		}
	}

	//写入跳过的文件
	fSkip, fskipErr := os.OpenFile(outputDir+"skip_files.txt", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if fskipErr != nil {
		return
	}
	_, err = fSkip.Write([]byte(strings.Join(skipFiles, "\n")))
	fSkip.Close()

}

func isDeprecatedFile(filePath string) bool {
	deprecateFiles := []string{
		"data_source_huaweicloud_antiddos_v1",
		"data_source_huaweicloud_compute_availability_zones_v2",
		"data_source_huaweicloud_csbs_backup_policy_v1",
		"data_source_huaweicloud_csbs_backup_v1",
		"data_source_huaweicloud_networking_network_v2",
		"data_source_huaweicloud_networking_subnet_v2",
		"data_source_huaweicloud_vbs_backup_policy_v2",
		"data_source_huaweicloud_vbs_backup_v2",
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
		"resource_huaweicloud_networking_floatingip_v2",
		"resource_huaweicloud_networking_floatingip_associate_v2",
		"resource_huaweicloud_networking_network_v2",
		"resource_huaweicloud_networking_router_interface_v2",
		"resource_huaweicloud_networking_router_route_v2",
		"resource_huaweicloud_networking_router_v2",
		"resource_huaweicloud_networking_subnet_v2",
		"resource_huaweicloud_vbs_backup_policy_v2",
		"data_source_huaweicloud_cts_tracker_v1",
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

func removeDuplicateValues(array []string) []string {
	keys := make(map[string]bool)
	list := []string{}

	for _, entry := range array {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func parseResourceFile(resourceName string, filePath string, file *ast.File, fset *token.FileSet, publicFuncs []string) (resourceName2 string, description string, allURI []CloudUri) {
	//先找到使用SDK的地方
	sdkPackages := make(map[string]string) //key： 先取别名>包名
	for _, d := range file.Imports {
		fullPath := strings.Trim(d.Path.Value, `"`)
		if strings.Contains(fullPath, sdkFilePreFix) {
			alias := fullPath[strings.LastIndex(fullPath, "/")+1:]

			if d.Name != nil {
				alias = d.Name.Name
			}
			sdkPackages[alias] = fullPath
		}
	}

	resourceFilebytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatal(err)
	}

	allResourceFileFunc := findAllFunc(file, fset)

	allURI = findAllURI(sdkPackages, resourceFilebytes, allResourceFileFunc, fset, publicFuncs)

	//fmt.Println(allURI)
	//再找直接调用rest的地方

	// 获得 config.DcsV2Client( 方法   resource_huaweicloud_dcs_instance_v1

	// 在 conig文件中，使用resourceType 匹配出catogery等基础信息

	return resourceName, "", allURI
}

func findAllFunc(f *ast.File, fset *token.FileSet) []*ast.FuncDecl {
	names := []string{}
	sort.Strings(names)

	funcs := []*ast.FuncDecl{}
	for _, d := range f.Decls {
		if fn, isFn := d.(*ast.FuncDecl); isFn {
			funcs = append(funcs, fn)

			//fmt.Println(fn.Name.Name, fn.Name.NamePos, set.Position(fn.Pos()), set.Position(fn.End()))
		}
	}
	// h
	return funcs
}

func findAllURI(sdkPackages map[string]string, resourceFileBytes []byte, funcDecls []*ast.FuncDecl, fset *token.FileSet, publicFuncs []string) (r []CloudUri) {
	rt := []CloudUri{}

	//key: funcName:clientDeclName
	//serviceClients := make(map[string]config.ServiceCatalog)

	//按照 方法匹配
	for _, fn := range funcDecls {
		rt = append(rt, findAllUriFromResourceFunc(fn, sdkPackages, resourceFileBytes, funcDecls, fset, publicFuncs)...)
		//	parserServiceClientSInfunc(fn, funcSrc, funcDecls, resourceFileBytes, fset, &serviceClients)

	}

	//对结果排序，去重
	return removeDuplicateCloudUri(rt)
}

func removeDuplicateCloudUri(array []CloudUri) []CloudUri {
	keys := make(map[string]CloudUri)
	list := []string{}
	rt := []CloudUri{}

	for _, v := range array {
		entry := v.url + v.httpMethod + v.resourceType
		entry = strings.ToLower(entry)
		if _, ok := keys[entry]; !ok {
			keys[entry] = v
			list = append(list, entry)
		}
	}
	sort.Strings(list)
	for i := 0; i < len(list); i++ {
		item := list[i]
		rt = append(rt, keys[item])
	}
	return rt
}

func findAllUriFromResourceFunc(curResourceFuncDecl *ast.FuncDecl, sdkPackages map[string]string,
	resourceFileBytes []byte, funcDecls []*ast.FuncDecl, fset *token.FileSet, publicFuncs []string) []CloudUri {

	startIndex := fset.Position(curResourceFuncDecl.Pos()).Offset
	endIndex := fset.Position(curResourceFuncDecl.End()).Offset
	//fmt.Printf("start:%d,end:%d,all%d \n", startIndex, endIndex, len(resourceFilebytes))
	funcSrc := string(resourceFileBytes[startIndex:endIndex])

	cloudUriArray := []CloudUri{}
	for alias, sdkFilePath := range sdkPackages {
		// 根据import的别名，匹配使用到的地方
		//1. client在前面定义的 eg: refinedAntiddos, err := antiddos.ListStatus(antiddosClient, listStatusOpts)
		reg := regexp.MustCompile(fmt.Sprintf(`(%s)\.(\w*)\((\w*)(.*)`, alias))
		allSubMatch := reg.FindAllStringSubmatch(funcSrc, -1)
		for i := 0; i < len(allSubMatch); i++ {
			//0:全部字符串，1：第一个submatch ...
			//methodInvokeIndexStart, methodInvokeIndexEnd := allSubMatchIndex[i][0], allSubMatchIndex[i][1]
			//sdkFunctionNameIndexStart, sdkFunctionNameIndexEnd := allSubMatchIndex[i][4], allSubMatchIndex[i][5]
			//clientBeenUsedIndexStart, clientBeenUsedIndexEnd := allSubMatchIndex[i][6], allSubMatchIndex[i][7]

			//methodInvoke := funcStr[methodInvokeIndexStart:methodInvokeIndexEnd]
			sdkFunctionName := allSubMatch[i][2]
			clientBeenUsed := allSubMatch[i][3]
			log.Println("TO find:", curResourceFuncDecl.Name.Name, alias, clientBeenUsed, sdkFunctionName)

			cloudUri := parseUriFromSdk(sdkFilePath, sdkFunctionName)
			//只有在sdk中匹配到的，才是有效的
			if cloudUri.url != "" {
				//2. 根据这里使用到的client ，向上找最近的一个 serviceClient定义,并根据它找到 resourceType,version等信息
				clientName, err := parseClientDecl(string(clientBeenUsed), funcSrc, curResourceFuncDecl, resourceFileBytes, funcDecls, fset)
				if err != nil {
					log.Println("found none client declare,so skip:", clientBeenUsed, funcSrc)
					cloudUri.resourceType = "unknow:" + clientBeenUsed
				} else {
					//在config.go中获得 catgegoryName
					categoryName := getCategoryFromConfig(clientName)
					log.Println("categoryName:", categoryName, " find by=", clientName)

					serviceCategory := parseEndPointByClient(categoryName)
					cloudUri.resourceType = serviceCategory.Name
					cloudUri.serviceCatalog = serviceCategory
				}

				//特殊处理tags类的调用
				newCloudUri := replaceTagUri(allSubMatch[i], cloudUri.url)
				if newCloudUri != "" {
					cloudUri.url = newCloudUri
				}
				cloudUriArray = append(cloudUriArray, cloudUri)
			} else {
				log.Println("parseUriFromSdk.return empty", sdkFunctionName, sdkFilePath)
			}

		}
		//2. client 直接定义在方法里 TODO
	}

	//使用utils中tags相关请求的，特殊处理url
	tagCloudUriArray := parseTagUriInFunc(funcSrc, curResourceFuncDecl, resourceFileBytes, funcDecls, fset)
	cloudUriArray = append(cloudUriArray, tagCloudUriArray...)
	return cloudUriArray
}

func replaceTagUri(allSubMatch []string, url string) string {
	newUrl := ""
	if allSubMatch[1] == "tags" && len(allSubMatch) > 4 {
		log.Println("replace tag:", allSubMatch[4])
		reg := regexp.MustCompile(`,\s"(.*)"`)
		subMatch := reg.FindAllStringSubmatch(allSubMatch[4], 1)
		if len(subMatch) > 0 {
			serviceTag := subMatch[0][1]
			newUrl = strings.Replace(url, "{resourceType}", serviceTag, -1)
		}
	}

	return newUrl
}

func parseTagUriInFunc(funcSrc string, curResourceFuncDecl *ast.FuncDecl, resourceFileBytes []byte,
	funcDecls []*ast.FuncDecl, fset *token.FileSet) []CloudUri {
	cloudUriArray := []CloudUri{}

	// utils.UpdateResourceTags(computeClient, d, "cloudservers", serverId)
	reg := regexp.MustCompile(`utils\.UpdateResourceTags\((\w*),\s(\w*),\s"(.*)",\s(.*)\)`)
	allSubMatch := reg.FindAllStringSubmatch(funcSrc, -1)
	if len(allSubMatch) > 0 {
		clientBeenUsed := allSubMatch[0][1]
		serviceType := allSubMatch[0][3]
		log.Println("parse tag:", clientBeenUsed, serviceType, funcSrc)
		tagUri := []CloudUri{
			{url: serviceType + "/{id}/tags/action", httpMethod: "POST", operationId: "batchUpdate"},
		}

		for i := 0; i < len(tagUri); i++ {

			cloudUri := tagUri[i]

			if cloudUri.url != "" {
				//2. 根据这里使用到的client ，向上找最近的一个 serviceClient定义,并根据它找到 resourceType,version等信息
				clientName, err := parseClientDecl(string(clientBeenUsed), funcSrc, curResourceFuncDecl, resourceFileBytes, funcDecls, fset)
				if err != nil {
					log.Println("found none client declare,so skip:", clientBeenUsed, funcSrc)
					cloudUri.resourceType = "unknow:" + clientBeenUsed
				} else {
					//在config.go中获得 catgegoryName
					categoryName := getCategoryFromConfig(clientName)
					log.Println("categoryName:", categoryName, " find by=", clientName)

					serviceCategory := parseEndPointByClient(categoryName)
					cloudUri.resourceType = serviceCategory.Name
					cloudUri.serviceCatalog = serviceCategory
				}

				cloudUriArray = append(cloudUriArray, cloudUri)
			}

		}
	}
	return cloudUriArray
}

func parseUriFromSdk(sdkFilePath string, sdkFunctionName string) (r CloudUri) {
	//TODO 从 vendor/github.com/terraform-providers/golangsdk/openstack/deh/v1/hosts/requests.go
	sdkFileDir := "./vendor/" + sdkFilePath + "/"

	cUri := getUriFromRequestFile(sdkFileDir, sdkFunctionName, true)
	fmt.Println("mmmm", cUri.url, cUri.httpMethod, sdkFunctionName, sdkFileDir)
	r.url = cUri.url
	r.httpMethod = cUri.httpMethod
	r.operationId = sdkFunctionName
	r.filePath = sdkFilePath
	return r
}

func TestParseUriFromSdk(t *testing.T) {
	path := "github.com/chnsz/golangsdk/openstack/networking/v2/ports"

	uri := parseUriFromSdk(path, "Update")
	fmt.Println("TestConfig:", uri)
	fmt.Printf("TestConfig:%#v", urlSupportsInRequestFile)
	fmt.Printf("TestConfig:%#v", urlSupportsInUriFile)
}

var clientDeclInConfig = make(map[string]string)

func getCategoryFromConfig(clientName string) string {
	v, ok := clientDeclInConfig[clientName]
	if ok {
		return v
	}
	return ""
}

func TestConfig(t *testing.T) {
	configFilePath = basePath + "huaweicloud/config/config.go"
	parseConfigFile(configFilePath)
	uri := getCategoryFromConfig("DnsV2Client")
	fmt.Println("TestConfig:", uri)
}

func parseConfigFile(filePath string) {
	set := token.NewFileSet()
	f, err := parser.ParseFile(set, filePath, nil, 0)
	if err != nil {
		log.Println("Failed to parse file:", filePath, err)
		return
	}

	resourceFilebytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatal(err)
	}

	for _, d := range f.Decls {
		if fn, isFn := d.(*ast.FuncDecl); isFn {
			startIndex := set.Position(fn.Pos()).Offset
			endIndex := set.Position(fn.End()).Offset
			funcSrc := string(resourceFilebytes[startIndex:endIndex])
			funcName := fn.Name.Name
			//判断是否有 return c.NewServiceClient("elb", region)
			reg := regexp.MustCompile(`NewServiceClient\("(.*)"`)
			submatch := reg.FindAllStringSubmatch(funcSrc, -1)
			if len(submatch) < 1 {
				log.Println("parse config error,searching regxp:", reg, "in func:", funcSrc[:50])
			} else {
				for i := 0; i < len(submatch); i++ {
					categoryName := submatch[i][1]
					clientDeclInConfig[funcName] = categoryName
				}
			}

		}

	}
}

func parseClientDecl(clientBeenUsed string, funcSrc string, curResourceFuncDecl *ast.FuncDecl, resourceFileBytes []byte, funcDecls []*ast.FuncDecl, fset *token.FileSet) (string, error) {
	var clientName string
	//先从入参中查找，
	funcFirstLineSrc := string(resourceFileBytes[fset.Position(curResourceFuncDecl.Pos()).Offset:fset.Position(curResourceFuncDecl.Body.Pos()).Offset])
	regInArgs := regexp.MustCompile(fmt.Sprintf(`.*(\s*%s\s\*golangsdk.ServiceClient)`, clientBeenUsed))
	// regInArgs := regexp.MustCompile(fmt.Sprintf(`.*(\s*%s\s*\S*)`, clientBeenUsed))
	isClientInArgs := regInArgs.MatchString(funcFirstLineSrc)
	if isClientInArgs {
		tpStr := regInArgs.FindAllString(funcFirstLineSrc, 1)[0]
		argsIndex := strings.Count(tpStr, ",") + 1
		//遍历方法，找到body体里有调用的
		clientUsedInInvoke, funcSrcInInvoke, funcInvoke, ok := parseMethodbeenInvoke(curResourceFuncDecl.Name.Name, argsIndex, resourceFileBytes, funcDecls, fset)
		if ok {
			return parseClientDecl(clientUsedInInvoke, funcSrcInInvoke, funcInvoke, resourceFileBytes, funcDecls, fset)
		} else {
			return "", fmt.Errorf("unfound the client:%s been init in this file body:%s", clientBeenUsed, funcSrc)
		}
	} else {
		//在当前body中匹配 antiddosClient, err := config.AntiDDosV1Client(GetRegion(d, config)) 匹配到AntiDDosV1Client
		reg := regexp.MustCompile(fmt.Sprintf(`%s\s*,\s*\w*\s*:?=\s*\w*\.(\w*)`, clientBeenUsed))
		allSubMatch := reg.FindAllStringSubmatch(funcSrc, 1)
		if len(allSubMatch) < 1 {
			//dnsClient, zoneType, err := chooseDNSClientbyZoneID(d, zoneID, meta) 特殊处理的client定义
			specailClient := parseSpecialClientDecl(clientBeenUsed, funcSrc)
			if specailClient != "" {
				return specailClient, nil
			}
			fmt.Println("没有到找到定义serviceClient的地方", clientBeenUsed, funcSrc)
			return "", fmt.Errorf("unfound the client:%s in func body:%s", clientBeenUsed, funcSrc)
		}

		clientName = allSubMatch[0][1]
		return clientName, nil
	}

}

//dnsClient, zoneType, err := chooseDNSClientbyZoneID(d, zoneID, meta) 特殊处理的client定义
func parseSpecialClientDecl(clientBeenUsed string, funcSrc string) string {
	reg := regexp.MustCompile(fmt.Sprintf(`%s.*:=\schooseDNSClientbyZoneID`, clientBeenUsed))
	isMatch := reg.MatchString(funcSrc)
	if isMatch {
		return "DnsV2Client"
	}
	return ""
}

//
func parseMethodbeenInvoke(funcName string, argsIndex int, resourceFileBytes []byte, funcDecls []*ast.FuncDecl, fset *token.FileSet) (clientBeenUsed string, funcSrc string, curResourceFuncDecl *ast.FuncDecl, exist bool) {
	exist = false

	for _, fn := range funcDecls {
		if fn.Name.Name == funcName {
			continue
		}
		startIndex := fset.Position(fn.Pos()).Offset
		endIndex := fset.Position(fn.End()).Offset
		funcSrc = string(resourceFileBytes[startIndex:endIndex])
		curResourceFuncDecl = fn

		reg := regexp.MustCompile(fmt.Sprintf(`%s\((.*)\)`, funcName))
		//reg := regexp.MustCompile(fmt.Sprintf(`%s\((\w*),`, funcName))
		allSubMatch := reg.FindAllStringSubmatch(funcSrc, 1)
		if len(allSubMatch) > 0 {

			argStr := allSubMatch[0][1]
			fmt.Println("parseMethodbeenInvoke.agrs", argStr, argsIndex)
			args := strings.Split(argStr, ",")
			if len(args) >= argsIndex {
				arg := strings.Trim(args[argsIndex-1], " ")
				clientBeenUsed = arg
				fmt.Println("parseMethodbeenInvoke.agrs2", clientBeenUsed)
				exist = true
				return
			}
		}

	}
	return
}

/*
根据字符index，判断属于哪个func,并返回对应func的源码 findAllUriFromResourceFunc
*/
/* func parseCurrentResourceFucntion(index int, funcDecls []*ast.FuncDecl, fset *token.FileSet) string {
	// for _, funcDecl := range funcDecls {
	// 	startIndex := fset.Position(funcDecl.Pos()).Offset
	// }
} */

type CloudUri struct {
	url            string
	httpMethod     string
	resourceType   string
	operationId    string
	filePath       string
	serviceCatalog config.ServiceCatalog
}

func buildYaml(resourceName, description string, cloudUri []CloudUri) string {
	var tags = []string{}

	var paths string
	//fmt.Printf("CloudUri:%#v", cloudUri)
	for i := 0; i < len(cloudUri); i++ {

		oneUrlParam := cloudUri[i]

		tags = append(tags, fmt.Sprintf("\n  - name: %s", oneUrlParam.resourceType))

		resourceBase := "/" + oneUrlParam.serviceCatalog.Version + "/"

		if !oneUrlParam.serviceCatalog.WithOutProjectID {
			resourceBase = resourceBase + "{project_id}/"
		}

		if oneUrlParam.serviceCatalog.ResourceBase != "" {
			resourceBase = resourceBase + oneUrlParam.serviceCatalog.ResourceBase + "/"
		}

		isSameWithPre := isSameWithPre(cloudUri, i)
		if isSameWithPre {
			var yamlTemplate = fmt.Sprintf(`
    %s:
      tag: %s
      operationId: %s`, oneUrlParam.httpMethod,
				oneUrlParam.resourceType, oneUrlParam.operationId)
			paths = paths + yamlTemplate
		} else {
			var yamlTemplate = fmt.Sprintf(`
  %s:
    %s:
      tag: %s
      operationId: %s`, resourceBase+oneUrlParam.url, oneUrlParam.httpMethod,
				oneUrlParam.resourceType, oneUrlParam.operationId)
			paths = paths + yamlTemplate
		}
	}
	fmt.Println("tags.length2:", len(tags))
	tags = removeDuplicateValues(tags)
	fmt.Println("tags.length3:", len(tags))

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
`, version, strings.Replace(resourceName, "huaweicloud", provider, -1), description, strings.Join(tags, ""), paths)
	return yamlTemplate
}

//未迁移至sdk的资源
func waitingUpdateResource(resourceName string) bool {
	deprecateFiles := []string{
		"data_source_huaweicloud_cdm_flavors_v1",
		"data_source_huaweicloud_gaussdb_mysql_flavors",
		"data_source_huaweicloud_obs_bucket_object",
		"data_source_huaweicloud_rds_flavors_v3",
		"resource_huaweicloud_cdm_cluster_v1",
		"resource_huaweicloud_cloudtable_cluster_v2",
		"resource_huaweicloud_dws_cluster",
		"resource_huaweicloud_ges_graph_v1",
		"resource_huaweicloud_mls_instance",
		"resource_huaweicloud_nat_dnat_rule_v2",
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

func parseEndPointByClient(clientName string) (r config.ServiceCatalog) {
	return config.AllServiceCatalog[clientName]
}

//创建一个新的endpointFile, export 变量 allServiceCatalog
func buildNewEndPointFile(filePath string) {

	resourceFilebytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatal(err)
	}

	set := token.NewFileSet()
	f, err := parser.ParseFile(set, filePath, nil, 0)
	if err != nil {
		log.Println("Failed to parse file:", filePath, err)
		return
	}

	for _, object := range f.Scope.Objects {
		if object.Name == "allServiceCatalog" && object.Kind == ast.Var {
			valueDecls := object.Decl.(*ast.ValueSpec)
			//只认基础字符类型
			startIndex := set.Position(valueDecls.Values[0].Pos()).Offset
			endIndex := set.Position(valueDecls.Values[0].End()).Offset

			funcSrc := string(resourceFilebytes[startIndex:endIndex])

			funcSrc = "\n var AllServiceCatalog = " + funcSrc
			funcSrcByte := []byte(funcSrc)
			resourceFilebytes = append(resourceFilebytes, funcSrcByte...)
			ioutil.WriteFile(filePath, resourceFilebytes, os.ModeAppend)
			fmt.Println(startIndex, endIndex, funcSrc)
		}
	}

}

func TestEndpoint(t *testing.T) {
	filePath := "/home/hm/go/src/github.com/terraform-providers/terraform-provider-huaweicloud/huaweicloud/config/endpoints.go"
	buildNewEndPointFile(filePath)

}

//保存 openstack/instances.{func} : uri
var urlSupportsInUriFile = make(map[string]string)
var urlSupportsInRequestFile = make(map[string]CloudUri)

func parseUriFromUriFile(filePath string) {
	set := token.NewFileSet()
	f, err := parser.ParseFile(set, filePath, nil, 0)
	if err != nil {
		log.Println("Failed to parse file:", filePath, err)
		return
	}

	//fmt.Printf("func: %v", getFuncList(f, ast.Con, true))

	varInPacks := make(map[string]string)
	for _, object := range f.Scope.Objects {
		if object.Kind == ast.Var || object.Kind == ast.Con {
			valueDecls := object.Decl.(*ast.ValueSpec)
			for i := 0; i < len(valueDecls.Names); i++ {
				name := valueDecls.Names[i].Name
				if len(valueDecls.Values) > i {
					//只认基础字符类型
					vExpr := valueDecls.Values[i].(*ast.BasicLit)
					varInPacks[name] = strings.ReplaceAll(vExpr.Value, `"`, ``)
				}
			}
			//ast.Print(set, object)
		}
	}

	resourceFilebytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatal(err)
	}

	for _, d := range f.Decls {
		if fn, isFn := d.(*ast.FuncDecl); isFn {
			startIndex := set.Position(fn.Pos()).Offset
			endIndex := set.Position(fn.End()).Offset

			funcSrc := string(resourceFilebytes[startIndex:endIndex])
			funcName := fn.Name.Name
			//判断是否有 client.ServiceURL(resourcePath, id, passwordPath)
			reg := regexp.MustCompile(`\.ServiceURL\((.*)\)`)
			submatch := reg.FindAllStringSubmatch(funcSrc, 1) //只取第一个

			argStartIndex := 0
			//这里有个奇葩的 cce，特殊处理下,argStartIndex 需要偏移2位

			if len(submatch) == 0 {
				reg := regexp.MustCompile(`return\s.*CCEServiceURL\((.*)\)`)
				submatch2 := reg.FindAllStringSubmatch(funcSrc, 1) //只取第一个
				if len(submatch2) > 0 {
					log.Printf("cceClient1:%v,%s", submatch2, funcSrc)
					log.Printf("cceClient2:%v,%s", submatch2, funcSrc)
					log.Printf("cceClient2.length:%d", len(submatch2[0]))
					submatch = submatch2
					argStartIndex = 2
				}
			}
			var uri = ""
			log.Printf("cceClient:%v", submatch)
			for i := 0; i < len(submatch); i++ {
				paramStr := submatch[i][1]
				params := strings.Split(paramStr, ",")
				paramValues := []string{}
				for j := argStartIndex; j < len(params); j++ {
					key := strings.TrimSpace(params[j])
					isString := strings.Contains(key, `"`)
					key = strings.ReplaceAll(key, `"`, ``)

					if isString {
						paramValues = append(paramValues, key)
					} else {
						v, ok := varInPacks[key]
						if ok || isString {
							paramValues = append(paramValues, v)
						} else {
							if strings.HasSuffix(key, ".ProjectID") {
								key = "project_id"
							}
							paramValues = append(paramValues, fmt.Sprintf(`{%s}`, key))
						}

					}

					uri = strings.Join(paramValues, "/")

				}
			}

			//	fmt.Println(funcName, uri)

			urlSupportsInUriFile[filePath+"."+funcName] = uri

			//对于循环调用的 ，这里简单处理
			if uri == "" {
				regSp := regexp.MustCompile(`return\s(\w+)\(`)
				submatch2 := regSp.FindAllStringSubmatch(funcSrc, 1) //只取第一个
				if len(submatch2) > 0 {
					name := submatch2[0][1]
					//使用被引用的方法的URL
					urlSupportsInUriFile[filePath+"."+funcName] = urlSupportsInUriFile[filePath+"."+name]
				}

			}

			if uri == "" {
				log.Println("parse URL failed,method=", funcName, " filePath:", filePath)
			}
			//fmt.Println("fff:", funcSrc)
		}
	}
}

/*
先从map中获取，没有则重新解析文件
*/
func getUriFromUriFile(filePath string, funcName string, isParsefile bool) string {
	v, ok := urlSupportsInUriFile[filePath+"."+funcName]
	if ok {
		return v
	}
	if isParsefile {
		parseUriFromUriFile(filePath)
		return getUriFromUriFile(filePath, funcName, false)
	}
	return ""
}

func getUriFromRequestFile(sdkFileDir string, funcName string, isParsefile bool) CloudUri {
	v, ok := urlSupportsInRequestFile[sdkFileDir+"."+funcName]
	if ok {
		return v
	}
	if isParsefile {
		parseUriFromRequestFile(sdkFileDir)
		return getUriFromRequestFile(sdkFileDir, funcName, false)
	}
	return CloudUri{}
}

func parseUriFromRequestFile(sdkFileDir string) {
	set := token.NewFileSet()
	requestFileNames := []string{"requests.go", "request.go"}
	var requestFilePath string

	for _, v := range requestFileNames {
		if filePathExists(sdkFileDir + v) {
			requestFilePath = sdkFileDir + v
			break
		}
	}

	if requestFilePath == "" {
		log.Println("[ERROR] cant find the requests files in ", sdkFileDir)
		return
	}

	f, err := parser.ParseFile(set, requestFilePath, nil, 0)
	if err != nil {
		log.Println("Failed to parse file:", requestFilePath, err)
		return
	}

	resourceFilebytes, err := ioutil.ReadFile(requestFilePath)
	if err != nil {
		log.Fatal(err)
	}

	//fmt.Printf("func: %v", getFuncList(f, ast.Con, true))
	funcNotDirectUseURLs := []*ast.FuncDecl{}
	urlSupportsInCurrentFile := []string{}

	var uriFilePath string
	urlsFileNames := []string{"urls.go", "url.go", "utils.go"}
	for _, v := range urlsFileNames {
		if filePathExists(sdkFileDir + v) {
			uriFilePath = sdkFileDir + v
			break
		}
	}

	if uriFilePath == "" {
		log.Println("[ERROR] cant find the url files", sdkFileDir)
		return
	}

	for _, d := range f.Decls {
		if fn, isFn := d.(*ast.FuncDecl); isFn {
			startIndex := set.Position(fn.Pos()).Offset
			endIndex := set.Position(fn.End()).Offset

			funcSrc := string(resourceFilebytes[startIndex:endIndex])
			funcName := fn.Name.Name
			//判断是否有 client.Post(createURL(client), b)
			reg1 := regexp.MustCompile(`\.(Head|Get|Post|Put|Patch|Delete|DeleteWithBody|DeleteWithResponse|DeleteWithBodyResp)\((\w*)\(`)
			//判断是否是client.Put(updateURL, b）

			reg2 := regexp.MustCompile(`\.(Head|Get|Post|Put|Patch|Delete|DeleteWithBody|DeleteWithResponse|DeleteWithBodyResp)\((\w*),`)

			//判断是否是 pagination.NewPager(c, rootURL(c),
			reg3 := regexp.MustCompile(`NewPager\(\w*,\s*(\w*)\(`)
			//判断是否是 pagination.NewPager(c, u,
			reg4 := regexp.MustCompile(`NewPager\(\w*,\s*(\w*),`)

			submatch1 := reg1.FindAllStringSubmatch(funcSrc, -1)
			submatch2 := reg2.FindAllStringSubmatch(funcSrc, -1)
			submatch3 := reg3.FindAllStringSubmatch(funcSrc, -1)
			submatch4 := reg4.FindAllStringSubmatch(funcSrc, -1)

			urlDeclInFunc := false

			if len(submatch1) > 0 {
				urlDeclInFunc = true
				for i := 0; i < len(submatch1); i++ {
					httpMethod := submatch1[i][1]
					urlFunc := submatch1[i][2]
					//判断
					//	mapToUrl(urlFunc,urlFilePath)
					//	println("ososo:", httpClientMethod, urlFunc)
					log.Println("[DEBUG]parseUriFromRequestFile.currentLine", funcName, urlFunc)
					//	fmt.Println("request path:", filePath, "uriFilePath:", uriFilePath)
					uri := getUriFromUriFile(uriFilePath, urlFunc, true)
					//	fmt.Println(funcName, uri)
					cloudUri := new(CloudUri)
					cloudUri.url = uri
					cloudUri.httpMethod = mapToStandardHttpMethod(httpMethod)
					urlSupportsInRequestFile[sdkFileDir+"."+funcName] = *cloudUri
					urlSupportsInCurrentFile = append(urlSupportsInCurrentFile, funcName)
				}
			} else if len(submatch2) > 0 {
				urlDeclInFunc = true
				for i := 0; i < len(submatch2); i++ {
					httpMethod := submatch2[i][1]
					urlFunc := submatch2[i][2]
					regUrLDecl := regexp.MustCompile(fmt.Sprintf(`%s\s*:=\s*(\w*)`, urlFunc))
					log.Println("[DEBUG]parseUriFromRequestFile.searchUrlDecl.currentfile", funcName, urlFunc)
					urlDeclsMatch := regUrLDecl.FindAllStringSubmatch(funcSrc, 1)
					if len(urlDeclsMatch) > 0 {
						urlFunc = urlDeclsMatch[0][1]
						uri := getUriFromUriFile(uriFilePath, urlFunc, true)
						cloudUri := new(CloudUri)
						cloudUri.url = uri
						cloudUri.httpMethod = mapToStandardHttpMethod(httpMethod)
						urlSupportsInRequestFile[sdkFileDir+"."+funcName] = *cloudUri
						urlSupportsInCurrentFile = append(urlSupportsInCurrentFile, funcName)
					} else {
						log.Println("[ERROR]failed find URL decl in request.go", fn.Name.Name, urlFunc)
					}

				}
			} else if len(submatch3) > 0 {
				urlDeclInFunc = true
				for i := 0; i < len(submatch3); i++ {
					httpMethod := "get"
					urlFunc := submatch3[i][1]
					//判断
					//	mapToUrl(urlFunc,urlFilePath)
					//	println("ososo:", httpClientMethod, urlFunc)
					log.Println("[DEBUG]parseUriFromRequestFile.currentLine", funcName, urlFunc)
					//	fmt.Println("request path:", filePath, "uriFilePath:", uriFilePath)
					uri := getUriFromUriFile(uriFilePath, urlFunc, true)
					//	fmt.Println(funcName, uri)
					cloudUri := new(CloudUri)
					cloudUri.url = uri
					cloudUri.httpMethod = httpMethod
					urlSupportsInRequestFile[sdkFileDir+"."+funcName] = *cloudUri
					urlSupportsInCurrentFile = append(urlSupportsInCurrentFile, funcName)
				}
			} else if len(submatch4) > 0 {
				urlDeclInFunc = true
				for i := 0; i < len(submatch4); i++ {
					httpMethod := "get"
					urlFunc := submatch4[i][1]
					regUrLDecl := regexp.MustCompile(fmt.Sprintf(`%s\s*:=\s*(\w*)`, urlFunc))
					log.Println("[DEBUG]parseUriFromRequestFile.searchUrlDecl.currentfile", funcName, urlFunc)
					urlDeclsMatch := regUrLDecl.FindAllStringSubmatch(funcSrc, 1)
					if len(urlDeclsMatch) > 0 {
						urlFunc = urlDeclsMatch[0][1]
						uri := getUriFromUriFile(uriFilePath, urlFunc, true)
						cloudUri := new(CloudUri)
						cloudUri.url = uri
						cloudUri.httpMethod = httpMethod
						urlSupportsInRequestFile[sdkFileDir+"."+funcName] = *cloudUri
						urlSupportsInCurrentFile = append(urlSupportsInCurrentFile, funcName)
					} else {
						log.Println("[ERROR]failed find URL decl in request.go", fn.Name.Name, urlFunc)
					}

				}
			}

			if !urlDeclInFunc {
				funcNotDirectUseURLs = append(funcNotDirectUseURLs, fn)
			}

			//fmt.Println("fff:", funcSrc)
		}

	}

	//处理第一次没有匹配到的
	parseRequestFuncNotDirect(set, sdkFileDir, resourceFilebytes, funcNotDirectUseURLs, urlSupportsInCurrentFile)
}

//reg1 := regexp.MustCompile(`\.(Head|Get|Post|Put|Patch|Delete|DeleteWithBody|DeleteWithResponse|DeleteWithBodyResp)\((\w*)\(`)
func mapToStandardHttpMethod(srcHttpMethod string) string {
	switch srcHttpMethod {
	case "Head":
		return "head"
	case "Get":
		return "get"
	case "Post":
		return "post"
	case "Put":
		return "put"
	case "Patch":
		return "patch"
	case "Delete":
		return "delete"
	case "DeleteWithBody":
		return "delete"
	case "DeleteWithResponse":
		return "delete"
	case "DeleteWithBodyResp":
		return "delete"
	default:
		return srcHttpMethod
	}
}

func parseRequestFuncNotDirect(set *token.FileSet, sdkFileDir string, resourceFilebytes []byte, funcNotDirectUseURLs []*ast.FuncDecl, urlSupportsInCurrentFile []string) {
	regStr := fmt.Sprintf(`(%s)\(`, strings.Join(urlSupportsInCurrentFile, "|"))

	reg := regexp.MustCompile(regStr)
	for _, fn := range funcNotDirectUseURLs {
		startIndex := set.Position(fn.Pos()).Offset
		endIndex := set.Position(fn.End()).Offset

		funcSrc := string(resourceFilebytes[startIndex:endIndex])
		funcName := fn.Name.Name
		//判断是否有 有间接调用
		submatch := reg.FindAllStringSubmatch(funcSrc, -1)
		if len(submatch) > 0 {
			for i := 0; i < len(submatch); i++ {
				actualFuncName := submatch[i][1]

				v, ok := urlSupportsInRequestFile[sdkFileDir+"."+actualFuncName]
				if ok {
					cloudUri := new(CloudUri)
					cloudUri.url = v.url
					cloudUri.httpMethod = v.httpMethod
					urlSupportsInRequestFile[sdkFileDir+"."+funcName] = *cloudUri
				}

			}

		}
	}
}

func filePathExists(path string) bool {
	_, err := os.Stat(path) //os.Stat获取文件信息
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
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

	ioutil.WriteFile("resource_name.txt", []byte(strings.Join(rsNames, "\n")), 0644)
	ioutil.WriteFile("data_source_name.txt", []byte(strings.Join(dsNames, "\n")), 0644)
	return
}

func isExportResource(resourceFileName, provider string, rsNames []string, dsNames []string) bool {
	re3, _ := regexp.Compile(`^_v\d+$`)

	if strings.HasPrefix(resourceFileName, "resource_") {
		if len(rsNames) < 1 {
			return true
		}
		resourceFileName = strings.TrimPrefix(resourceFileName, "resource_")
		resourceFileName = strings.Replace(resourceFileName, "huaweicloud", provider, -1)
		for _, v := range rsNames {
			remaindStr := strings.TrimPrefix(v, resourceFileName)
			if remaindStr == "" || re3.MatchString(remaindStr) {
				return true
			}
		}
	}

	if strings.HasPrefix(resourceFileName, "data_source_") {
		if len(dsNames) < 1 {
			return true
		}
		resourceFileName = strings.TrimPrefix(resourceFileName, "data_source_")
		resourceFileName = strings.Replace(resourceFileName, "huaweicloud", provider, -1)
		for _, v := range dsNames {
			remaindStr := strings.TrimPrefix(v, resourceFileName)
			if remaindStr == "" || re3.MatchString(remaindStr) {
				return true
			}
		}
	}
	return false
}
