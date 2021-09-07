# terraform-api-scan

扫描使用到的sdk api

执行： bash scan.sh

1. 将下载github最新的release 版本
2. 将最新版本信息写入： last_version.info
3. 将输出路径写入： output_dir.info
4. 被忽略解析的文件：${output_dir}/skip_files.txt
