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

var clientDeclInConfig = make(map[string]string)

func getCategoryFromConfig(clientName string) string {
	v, ok := clientDeclInConfig[clientName]
	if ok {
		return v
	}
	return ""
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

			// 判断是否有 return c.NewServiceClient("elb", region)
			reg := regexp.MustCompile(`NewServiceClient\("(.*)"`)
			submatch := reg.FindAllStringSubmatch(funcSrc, -1)
			if len(submatch) < 1 {
				log.Println("skip parse config method:", funcName)
				continue
			}

			for i := 0; i < len(submatch); i++ {
				categoryName := submatch[i][1]
				clientDeclInConfig[funcName] = categoryName
			}
		}
	}

	//log.Println("[DEBUG] client config:", clientDeclInConfig)
}

// 解析资源文件的主入口
func parseResourceFile(resourceName string, filePath string, file *ast.File, fset *token.FileSet, publicFuncs []string,
	newResourceName string) (resourceName2 string, description string, allURI []CloudUri, rpath string, newResourceName2 string) {

	sdkFilePreFix := "github.com/chnsz/golangsdk/openstack/"
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

	return resourceName, "", allURI, filePath, newResourceName
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

func findAllUriFromResourceFunc(curResourceFuncDecl *ast.FuncDecl, sdkPackages map[string]string,
	resourceFileBytes []byte, funcDecls []*ast.FuncDecl, fset *token.FileSet, publicFuncs []string) []CloudUri {

	funcName := curResourceFuncDecl.Name.Name
	startIndex := fset.Position(curResourceFuncDecl.Pos()).Offset
	endIndex := fset.Position(curResourceFuncDecl.End()).Offset
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

			if strings.HasPrefix(sdkFunctionName, "Extract") {
				log.Printf("[DEBUG] skip to parse %s.%s\n", alias, sdkFunctionName)
				continue
			}

			log.Printf("find function %s used %s.%s with %s\n", funcName, alias, sdkFunctionName, clientBeenUsed)
			cloudUri := parseUriFromSdk(sdkFilePath, sdkFunctionName)
			//只有在sdk中匹配到的，才是有效的
			if cloudUri.url != "" {
				//2. 根据这里使用到的client ，向上找最近的一个 serviceClient定义,并根据它找到 resourceType,version等信息
				clientName, err := parseClientDecl(string(clientBeenUsed), funcSrc, curResourceFuncDecl, resourceFileBytes, funcDecls, fset)
				if err != nil {
					log.Printf("[WARN] found none client declares %s, so skip %s\n", clientBeenUsed, funcName)
					cloudUri.resourceType = "unknown"
				} else {
					//在config.go中获得 catgegoryName
					categoryName := getCategoryFromConfig(clientName)
					log.Printf("[DEBUG] service category of %s is %s", clientName, categoryName)

					if serviceCategory := parseEndPointByClient(categoryName); serviceCategory != nil {
						cloudUri.resourceType = serviceCategory.Name
						cloudUri.serviceCatalog = *serviceCategory
					} else {
						log.Printf("[ERROR] can not find service catalog of %s\n", categoryName)
					}
				}

				// 特殊处理 golangsdk/openstack/common/tags 包的调用
				// 1. 替换 {resourceType} 变量
				// 2. 在URL中增加projectID --- WithOutProjectID = false
				newCloudUri := replaceTagUri(allSubMatch[i], cloudUri.url)
				if newCloudUri != "" {
					cloudUri.url = newCloudUri
					cloudUri.serviceCatalog.WithOutProjectID = false
				}
				cloudUriArray = append(cloudUriArray, cloudUri)
			} else {
				log.Println("parseUriFromSdk.return empty", sdkFunctionName, sdkFilePath)
			}
		}
		// TODO: client 直接定义在方法里
	}

	// 使用 huaweicloud/utils包中tags 相关请求的，特殊处理url
	tagCloudUriArray := parseTagUriInFunc(funcSrc, curResourceFuncDecl, resourceFileBytes, funcDecls, fset)
	cloudUriArray = append(cloudUriArray, tagCloudUriArray...)
	return cloudUriArray
}

func replaceTagUri(allSubMatch []string, url string) string {
	if allSubMatch[1] == "tags" && len(allSubMatch) > 4 {
		log.Printf("[DEBUG] parse tags URL in `%s`\n", allSubMatch[0])
		reg := regexp.MustCompile(`,\s"(.*)",`)
		subMatch := reg.FindStringSubmatch(allSubMatch[4])
		if len(subMatch) > 1 {
			serviceTag := subMatch[1]
			newUrl := strings.Replace(url, "{resourceType}", serviceTag, -1)
			log.Printf("[DEBUG] update tags URL from %s to %s\n", url, newUrl)
			return newUrl
		}
		log.Printf("[DEBUG] the tags URL(%s) is not changed\n", url)
		return url
	}

	return ""
}

func parseTagUriInFunc(funcSrc string, curResourceFuncDecl *ast.FuncDecl, resourceFileBytes []byte,
	funcDecls []*ast.FuncDecl, fset *token.FileSet) []CloudUri {
	cloudUriArray := []CloudUri{}

	// utils.UpdateResourceTags(computeClient, d, "cloudservers", serverId)
	reg := regexp.MustCompile(`utils\.UpdateResourceTags\((\w*),\s(\w*),\s"(.*)",\s(.*)\)`)
	allSubMatch := reg.FindAllStringSubmatch(funcSrc, -1)
	if len(allSubMatch) > 0 {
		log.Printf("[DEBUG] parse tags URL in `%s`\n", allSubMatch[0][0])

		clientBeenUsed := allSubMatch[0][1]
		serviceType := allSubMatch[0][3]
		tagUri := []CloudUri{
			{url: serviceType + "/{id}/tags/action", httpMethod: "POST", operationId: "batchUpdate"},
		}

		for _, cloudUri := range tagUri {
			if cloudUri.url != "" {
				//2. 根据这里使用到的client ，向上找最近的一个 serviceClient定义,并根据它找到 resourceType,version等信息
				clientName, err := parseClientDecl(string(clientBeenUsed), funcSrc, curResourceFuncDecl, resourceFileBytes, funcDecls, fset)
				if err != nil {
					funcName := curResourceFuncDecl.Name.Name
					log.Printf("[WARN] found none client declares %s, so skip %s\n", clientBeenUsed, funcName)
					cloudUri.resourceType = "unknown"
				} else {
					//在config.go中获得 catgegoryName
					categoryName := getCategoryFromConfig(clientName)
					log.Printf("[DEBUG] service category of %s is %s", clientName, categoryName)

					if serviceCategory := parseEndPointByClient(categoryName); serviceCategory != nil {
						// 特殊处理 golangsdk/openstack/common/tags 包的调用
						// 在URL中增加projectID --- WithOutProjectID = false
						serviceCategory.WithOutProjectID = false
						cloudUri.resourceType = serviceCategory.Name
						cloudUri.serviceCatalog = *serviceCategory
					} else {
						log.Printf("[ERROR] can not find service catalog of %s\n", categoryName)
					}
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

	// 去除URL中的query参数
	if lastIndex := strings.Index(cUri.url, "?"); lastIndex > 0 {
		cUri.url = cUri.url[:lastIndex]
	}

	r.url = cUri.url
	r.httpMethod = cUri.httpMethod
	r.operationId = sdkFunctionName
	r.filePath = sdkFilePath
	return r
}

func parseClientDecl(clientBeenUsed string, funcSrc string, curResourceFuncDecl *ast.FuncDecl, resourceFileBytes []byte, funcDecls []*ast.FuncDecl, fset *token.FileSet) (string, error) {
	var clientName string

	// 先从入参中查找
	funcName := curResourceFuncDecl.Name.Name
	startIndex := fset.Position(curResourceFuncDecl.Pos()).Offset
	startBodyIndex := fset.Position(curResourceFuncDecl.Body.Pos()).Offset
	funcFirstLineSrc := string(resourceFileBytes[startIndex:startBodyIndex])

	regInArgs := regexp.MustCompile(fmt.Sprintf(`.*(\s*%s\s\*golangsdk.ServiceClient)`, clientBeenUsed))
	isClientInArgs := regInArgs.MatchString(funcFirstLineSrc)
	if isClientInArgs {
		tpStr := regInArgs.FindAllString(funcFirstLineSrc, 1)[0]
		argsIndex := strings.Count(tpStr, ",") + 1
		// 遍历方法，找到body体里有调用的
		clientUsedInInvoke, funcSrcInInvoke, funcInvoke, ok := parseMethodbeenInvoke(funcName, argsIndex, resourceFileBytes, funcDecls, fset)
		if ok {
			return parseClientDecl(clientUsedInInvoke, funcSrcInInvoke, funcInvoke, resourceFileBytes, funcDecls, fset)
		}

		return "", fmt.Errorf("cannot found the client %s in defination of function %s", clientBeenUsed, funcName)
	} else {
		// 在当前body中匹配 antiddosClient, err := config.AntiDDosV1Client(GetRegion(d, config)) 匹配到AntiDDosV1Client
		reg := regexp.MustCompile(fmt.Sprintf(`%s\s*,\s*\w*\s*:?=\s*\w*\.(\w*)`, clientBeenUsed))
		allSubMatch := reg.FindAllStringSubmatch(funcSrc, 1)
		if len(allSubMatch) < 1 {
			//dnsClient, zoneType, err := chooseDNSClientbyZoneID(d, zoneID, meta) 特殊处理的client定义
			specailClient := parseSpecialClientDecl(clientBeenUsed, funcSrc)
			if specailClient != "" {
				return specailClient, nil
			}
			fmt.Println("没有到找到定义serviceClient的地方", clientBeenUsed, funcSrc)
			return "", fmt.Errorf("cannot found the client %s in function %s body", clientBeenUsed, funcName)
		}

		clientName = allSubMatch[0][1]
		return clientName, nil
	}

}

// dnsClient, zoneType, err := chooseDNSClientbyZoneID(d, zoneID, meta) 特殊处理的client定义
func parseSpecialClientDecl(clientBeenUsed string, funcSrc string) string {
	reg := regexp.MustCompile(fmt.Sprintf(`%s.*:=\schooseDNSClientbyZoneID`, clientBeenUsed))
	isMatch := reg.MatchString(funcSrc)
	if isMatch {
		return "DnsV2Client"
	}
	return ""
}

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
			calledFuncName := curResourceFuncDecl.Name.Name
			fmt.Printf("parseMethodbeenInvoke.agrs: %s call %s(%s) %d\n", calledFuncName, funcName, argStr, argsIndex)
			args := strings.Split(argStr, ",")
			if len(args) >= argsIndex {
				arg := strings.Trim(args[argsIndex-1], " ")
				arg = strings.Trim(arg, ")")
				// 解析失败，直接return
				if strings.Contains(arg, "(") {
					log.Printf("[WARN] unable to parse the arguments in %s %s\n", calledFuncName, arg)
					return
				}

				clientBeenUsed = arg
				fmt.Println("parseMethodbeenInvoke.agrs2", calledFuncName, clientBeenUsed)
				exist = true
				return
			}
		}

	}
	return
}

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

	// 用于保存调用其他URL方法的函数
	callingFunc := make(map[string]string)

	for _, d := range f.Decls {
		if fn, isFn := d.(*ast.FuncDecl); isFn {
			startIndex := set.Position(fn.Pos()).Offset
			endIndex := set.Position(fn.End()).Offset

			funcSrc := string(resourceFilebytes[startIndex:endIndex])
			funcName := fn.Name.Name
			funckey := filePath + "." + funcName

			//判断是否有 client.ServiceURL(resourcePath, id, passwordPath)
			reg := regexp.MustCompile(`\.ServiceURL\((.*)\)`)
			submatch := reg.FindAllStringSubmatch(funcSrc, 1) //只取第一个

			argStartIndex := 0
			//这里有个奇葩的 cce，特殊处理下,argStartIndex 需要偏移2位

			if len(submatch) == 0 {
				reg := regexp.MustCompile(`return\s.*CCEServiceURL\((.*)\)`)
				submatch2 := reg.FindAllStringSubmatch(funcSrc, 1) //只取第一个
				if len(submatch2) > 0 {
					log.Printf("cceClient: %v, %s\n", submatch2, funcName)
					submatch = submatch2
					argStartIndex = 2
				}
			}

			if len(submatch) == 0 {
				regSp := regexp.MustCompile(`return\s(\w+)\(`)
				submatch2 := regSp.FindAllStringSubmatch(funcSrc, 1) //只取第一个
				if len(submatch2) > 0 {
					name := submatch2[0][1]
					callingFunc[funckey] = name
				} else {
					log.Printf("[WARN] can not parse the URL function %s in %s\n", funcName, filePath)
				}
				continue
			}

			paramStr := submatch[0][1]
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
			}
			uri := strings.Join(paramValues, "/")
			urlSupportsInUriFile[funckey] = uri

			//处理特殊URL
			if strings.Contains(funckey, "/dns/v2/ptrrecords/urls.go.baseURL") ||
				strings.Contains(funckey, "/dns/v2/ptrrecords/urls.go.resourceURL") {
				urlSupportsInUriFile[funckey] = "reverse/floatingips/{region}:{floatingip_id}"
			}
		}
	}

	// 处理函数调用的情况
	for key, funcName := range callingFunc {
		uri := urlSupportsInUriFile[filePath+"."+funcName]
		urlSupportsInUriFile[key] = uri
		if uri == "" {
			log.Printf("[WARN] can not parse the URL function %s/%s in %s\n", key, funcName, filePath)
		}
	}
}

/*
先从map中获取，没有则重新解析文件
*/
func getUriFromUriFile(filePath string, funcName string, isParsefile bool) string {
	if v, ok := urlSupportsInUriFile[filePath+"."+funcName]; ok {
		return v
	}

	if isParsefile {
		parseUriFromUriFile(filePath)
		return getUriFromUriFile(filePath, funcName, false)
	}

	log.Printf("[WARN] failed parsing URL of method %s in file %s\n", funcName, filePath)
	return ""
}

func getUriFromRequestFile(sdkFileDir string, funcName string, isParsefile bool) CloudUri {
	if v, ok := urlSupportsInRequestFile[sdkFileDir+"."+funcName]; ok {
		return v
	}

	if isParsefile {
		parseUriFromRequestFile(sdkFileDir)
		return getUriFromRequestFile(sdkFileDir, funcName, false)
	}

	log.Printf("[WARN] failed parsing cloud URL of method %s in package %s\n", funcName, sdkFileDir)
	return CloudUri{}
}

func parseUriFromRequestFile(sdkFileDir string) {
	set := token.NewFileSet()
	// most of all files are named requests.go
	// request.go is only in openstack/elb/v2/certificates package, will normalize it in golansdk
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
	// most of all files are named urls.go
	// url.go and utils.go are located in some packages, will normalize them in golansdk
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
					log.Println("[DEBUG] parseUriFromRequestFile-regmatch1", funcName, urlFunc)
					//	fmt.Println("request path:", filePath, "uriFilePath:", uriFilePath)
					uri := getUriFromUriFile(uriFilePath, urlFunc, true)
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
					log.Println("[DEBUG] parseUriFromRequestFile-regmatch2", funcName, urlFunc)

					regUrLDecl := regexp.MustCompile(fmt.Sprintf(`%s\s*:=\s*(\w*)`, urlFunc))
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
						log.Println("[ERROR] failed find URL decl in request.go", fn.Name.Name, urlFunc)
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
					log.Println("[DEBUG] parseUriFromRequestFile-regmatch3", funcName, urlFunc)
					//	fmt.Println("request path:", filePath, "uriFilePath:", uriFilePath)
					uri := getUriFromUriFile(uriFilePath, urlFunc, true)
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
					log.Println("[DEBUG] parseUriFromRequestFile-regmatch4", funcName, urlFunc)

					regUrLDecl := regexp.MustCompile(fmt.Sprintf(`%s\s*:=\s*(\w*)`, urlFunc))
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
						log.Println("[ERROR] failed find URL decl in request.go", fn.Name.Name, urlFunc)
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
