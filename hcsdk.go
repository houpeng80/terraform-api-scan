package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"regexp"
	"strings"
)

var clientConfig = make(map[string]string)

func getCategoryFromClientConfig(clientName string) string {
	v, ok := clientConfig[clientName]
	if ok {
		return v
	}

	// 通过client名称获取catalog
	reg := regexp.MustCompile(`^(\w+)Client$`)
	submatch := reg.FindStringSubmatch(clientName)
	if len(submatch) == 2 {
		return strings.ToLower(submatch[1])
	}

	return ""
}

func parseHCConfigFile(filePath string) {
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

			reg := regexp.MustCompile(`NewHcClient\(.*, "(.*)"`)
			submatch := reg.FindAllStringSubmatch(funcSrc, -1)
			if len(submatch) < 1 {
				log.Println("skip parse config method:", funcName)
				continue
			}

			for _, match := range submatch {
				clientConfig[funcName] = match[1]
			}
		}
	}

	//log.Println("[DEBUG] client config:", clientConfig)
}

// 解析资源文件的主入口
func parseResourceFile2(resourceName string, filePath string, file *ast.File, fset *token.FileSet, publicFuncs []string,
	newResourceName string) (resourceName2 string, description string, allURI []CloudUri, rpath string, newResourceName2 string) {

	// 先找到使用SDK的地方
	usedPackages := []string{}
	sdkPackages := make(map[string]string) //key： 先取别名>包名
	for _, d := range file.Imports {
		fullPath := strings.Trim(d.Path.Value, `"`)
		if strings.Contains(fullPath, "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/") {
			// 忽略 .../model 的包并去重
			realPath := strings.TrimSuffix(fullPath, "/model")
			if sliceContains(usedPackages, realPath) {
				continue
			}

			alias := realPath[strings.LastIndex(realPath, "/")+1:]
			if d.Name != nil {
				alias = d.Name.Name
			}
			sdkPackages[alias] = realPath
			usedPackages = append(usedPackages, realPath)
		}
	}

	log.Printf("==== importing sdk packages: %#v ====\n", sdkPackages)

	resourceFilebytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatal(err)
	}

	allResourceFileFunc := findAllFunc(file, fset)

	allURI = findAllURI2(sdkPackages, resourceFilebytes, allResourceFileFunc, fset, publicFuncs)

	return resourceName, "", allURI, filePath, newResourceName
}

func findAllURI2(sdkPackages map[string]string, resourceFileBytes []byte, funcDecls []*ast.FuncDecl, fset *token.FileSet,
	publicFuncs []string) (r []CloudUri) {

	rt := []CloudUri{}
	for _, fn := range funcDecls {
		rt = append(rt, findURIFromResourceFunc2(fn, sdkPackages, resourceFileBytes, funcDecls, fset, publicFuncs)...)
	}

	//对结果排序，去重
	return removeDuplicateCloudUri(rt)
}

func findURIFromResourceFunc2(curResourceFuncDecl *ast.FuncDecl, sdkPackages map[string]string,
	resourceFileBytes []byte, funcDecls []*ast.FuncDecl, fset *token.FileSet, publicFuncs []string) []CloudUri {

	startIndex := fset.Position(curResourceFuncDecl.Pos()).Offset
	endIndex := fset.Position(curResourceFuncDecl.End()).Offset
	funcSrc := string(resourceFileBytes[startIndex:endIndex])

	funcName := curResourceFuncDecl.Name.Name
	cloudUriArray := []CloudUri{}

	for _, sdkFilePath := range sdkPackages {
		// 找到所有使用client的方法 eg: response, err := client.AddAlarmRule(&createReq)
		// 或者 _, err := client.UpdateTask(&model.UpdateTaskRequest{
		reg := regexp.MustCompile(`= (.*[c|C]lient)\.(\w*)\(.*[)|{]`)
		allSubMatch := reg.FindAllStringSubmatch(funcSrc, -1)
		for i := 0; i < len(allSubMatch); i++ {
			// 0: 全部字符串, 1: client名称, 2: 方法名称
			clientBeenUsed := allSubMatch[i][1]
			sdkFunctionName := allSubMatch[i][2]
			log.Printf("find function %s used %s.%s\n", funcName, clientBeenUsed, sdkFunctionName)

			// 1. 根据方法名称找到对应的URI
			cloudUri := parseUriFromSdk2(sdkFilePath, sdkFunctionName)
			if cloudUri.url != "" {
				// 2. 根据使用到的client ，向上找最近的一个 Client定义, 并根据它找到 resourceType,version等信息
				clientName, err := parseClientDecl2(clientBeenUsed, funcSrc, curResourceFuncDecl, resourceFileBytes, funcDecls, fset)
				if err != nil {
					log.Println("found none client declares, so skip:", clientBeenUsed, funcName)
					cloudUri.resourceType = "unknown"
				} else {
					// 3. 找到client对应的catalog
					categoryName := getCategoryFromClientConfig(clientName)
					log.Printf("category of client %s is %s\n", clientName, categoryName)

					if serviceCategory := parseEndPointByClient(categoryName); serviceCategory != nil {
						cloudUri.resourceType = serviceCategory.Name
						cloudUri.serviceCatalog = *serviceCategory
					} else {
						log.Printf("[ERROR] can not find service catalog of %s\n", categoryName)
					}
				}

				cloudUriArray = append(cloudUriArray, cloudUri)
			} else {
				log.Println("parseUriFromSdk2 return empty", sdkFunctionName, sdkFilePath)
			}
		}
	}

	return cloudUriArray
}

func parseUriFromSdk2(sdkFilePath string, sdkFunctionName string) CloudUri {
	sdkFileDir := "./vendor/" + sdkFilePath + "/"

	cUri := getUriFromRequestFile2(sdkFileDir, sdkFunctionName, true)
	fmt.Println("mmmm", cUri.url, cUri.httpMethod, sdkFunctionName, sdkFileDir)

	return CloudUri{
		url:         cUri.url,
		httpMethod:  cUri.httpMethod,
		operationId: sdkFunctionName,
		filePath:    sdkFilePath,
	}
}

func getUriFromRequestFile2(sdkFileDir string, funcName string, firstTime bool) CloudUri {
	v, ok := urlSupportsInRequestFile[sdkFileDir+"."+funcName]
	if ok {
		return v
	}

	if firstTime {
		parseUriFromRequestFile2(sdkFileDir)
		return getUriFromRequestFile2(sdkFileDir, funcName, false)
	}

	log.Printf("[ERROR] can not find URL of %s in %s\n", funcName, sdkFileDir)
	return CloudUri{}
}

func getClientAndMetaFile(sdkDir string) (string, string) {
	var clientFile, metaFile string

	dir, err := ioutil.ReadDir(sdkDir)
	if err != nil {
		log.Printf("faild to read %s: %s\n", sdkDir, err)
		return "", ""
	}

	for _, fi := range dir {
		if fi.IsDir() { // 忽略目录
			continue
		}

		name := fi.Name()
		if strings.HasSuffix(name, "_client.go") {
			clientFile = sdkDir + "/" + name
		} else if strings.HasSuffix(name, "_meta.go") {
			metaFile = sdkDir + "/" + name
		}
	}
	return clientFile, metaFile
}

type HttpRequest struct {
	Method string
	URI    string
}

func parseUriFromRequestFile2(sdkFileDir string) {
	clientPath, metaPath := getClientAndMetaFile(sdkFileDir)
	if clientPath == "" || metaPath == "" {
		log.Println("[ERROR] cant find the requests files in ", sdkFileDir)
		return
	}

	metaSet := token.NewFileSet()
	f1, err := parser.ParseFile(metaSet, metaPath, nil, 0)
	if err != nil {
		log.Println("Failed to parse file:", metaPath, err)
		return
	}

	filebytes, err := ioutil.ReadFile(metaPath)
	if err != nil {
		log.Fatal(err)
	}

	var metaAPIs = make(map[string]*HttpRequest)
	for _, d := range f1.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}

		funcName := fn.Name.Name
		if !strings.HasPrefix(funcName, "GenReqDefFor") {
			continue
		}

		startIndex := metaSet.Position(fn.Pos()).Offset
		endIndex := metaSet.Position(fn.End()).Offset
		funcSrc := string(filebytes[startIndex:endIndex])

		reg1 := regexp.MustCompile(`WithMethod\(http.Method(\w*)\)`)
		reg2 := regexp.MustCompile(`WithPath\("(.*)"\)`)

		submatch1 := reg1.FindStringSubmatch(funcSrc)
		submatch2 := reg2.FindStringSubmatch(funcSrc)

		if len(submatch1) < 2 || len(submatch2) < 2 {
			continue
		}

		metaAPIs[funcName] = &HttpRequest{
			Method: submatch1[1],
			URI:    submatch2[1],
		}
	}
	log.Printf("API mapping in %s: %#v\n", sdkFileDir, metaAPIs)

	clientSet := token.NewFileSet()
	f2, err := parser.ParseFile(clientSet, clientPath, nil, 0)
	if err != nil {
		log.Println("Failed to parse file:", clientPath, err)
		return
	}

	resourceFilebytes, err := ioutil.ReadFile(clientPath)
	if err != nil {
		log.Fatal(err)
	}

	for _, d := range f2.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}

		funcName := fn.Name.Name
		startIndex := clientSet.Position(fn.Pos()).Offset
		endIndex := clientSet.Position(fn.End()).Offset
		funcSrc := string(resourceFilebytes[startIndex:endIndex])

		reg := regexp.MustCompile(`:= (GenReqDefFor\w*)\(\)`)
		submatch := reg.FindStringSubmatch(funcSrc)

		if len(submatch) < 2 {
			continue
		}
		metaFunc := submatch[1]
		requestInfo, ok := metaAPIs[metaFunc]
		if !ok || requestInfo == nil {
			log.Printf("%s is used by %s, but not defined in meta file", metaFunc, funcName)
			continue
		}

		urlSupportsInRequestFile[sdkFileDir+"."+funcName] = CloudUri{
			url:        requestInfo.URI,
			httpMethod: strings.ToLower(requestInfo.Method),
		}
	}

	return
}

func parseClientDecl2(client string, funcSrc string, curResourceFuncDecl *ast.FuncDecl, resourceFileBytes []byte,
	funcDecls []*ast.FuncDecl, fset *token.FileSet) (string, error) {

	// 先在定义中查找client, eg: client, err := config.HcAomV2Client(config.GetRegion(d))
	regInDef := regexp.MustCompile(fmt.Sprintf(`%s, \w+ := \w*.(.+Client)`, client))
	submatch := regInDef.FindStringSubmatch(funcSrc)
	if len(submatch) == 2 {
		return submatch[1], nil
	}

	// 再在参数中查找client
	regInArgs := regexp.MustCompile(fmt.Sprintf(`^func \w+\(.*%s *.+\.(.+Client)`, client))
	submatch2 := regInArgs.FindStringSubmatch(funcSrc)
	if len(submatch2) == 2 {
		return submatch2[1], nil
	}

	return "", fmt.Errorf("not found")
}
