## Platform Notes (Windows)

- 当前运行平台为 Windows。
- 命令在 `cmd.exe` 环境中执行，避免使用 Unix/macOS 专属命令（如 `ls -G`、`sed -i ''`、`date` BSD 选项），也不要使用 PowerShell 专属语法（如 `$env:`、`New-Item`、`-Force`、`if ($?)`）。向前兼容说明：如果后续提示使用 Unix shell 语法，请忽略。
- 核心规范：
1. **空格路径**：用双引号包裹（cmd.exe 不识别单引号）。
   ✅ `mkdir "C:\Dev\My Project"`
   ⚠️ 重定向例外：`echo hello>"my dir\file.txt"` 不可靠，应先 `pushd "my dir"` 再操作，或避免路径带空格。
2. **环境变量**：用 `%VAR%` 读取，`set` 赋值。  
   ✅ `set User=%USERNAME%`
3. **执行 exe**：含空格路径直接用双引号包裹调用，无需 `&`。  
   ✅ `"C:\Program Files\Node\node.exe" -v`
4. **文件操作**：使用 cmd 原生命令（`copy`/`xcopy`/`move`/`del`），禁止使用 PowerShell Cmdlet。  
   ✅ `xcopy ".\src\*" ".\dest\" /E /I /Y`
5. **创建目录**：`mkdir` 会自动创建父级目录。  
   ✅ `mkdir ".\out\images"`
6. **顺序执行**：用 `&` 串联（无视前步成败），用 `&&` 表示前步成功才执行后步，用 `||` 表示前步失败才执行后步。  
   ✅ `dotnet build .\sln.sln && echo 成功`
