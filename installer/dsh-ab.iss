; dsh-ab.iss - DSH-AB installer.
;
; Packages the payload assembled by build\mkpayload.ps1 into a single per-user installer:
;   * installs to %LOCALAPPDATA%\DSH-AB by default, no administrator rights needed
;   * refuses to install anywhere that is not an empty folder (never overwrite, never merge),
;     and refuses a port that is already taken on 127.0.0.1 - but not a second DSH-AB: two
;     installations are allowed to coexist
;   * there is no overwrite upgrade at all: upgrading means installing into a new empty folder, and
;     the old installation is either left alone or uninstalled separately
;   * every installation has its own identity - its own Add/Remove entry, its own Start Menu entry
;     and its own desktop shortcut, named by InstallName (DSH-AB while it is
;     the only one on this machine, DSH-AB (production port) when another one coexists, and
;     DSH-AB (tag) when that port is unusable or another installation claims it); uninstalling one
;     copy leaves every other copy untouched
;   * asks for the production port and writes it into dsh-ab.toml; an unattended install
;     passes /PRODUCTION= instead of answering the page
;   * installs slot-a and creates an empty slot-b: the copy target for the first change
;   * does no git operation at all: the AI that maintains the installation runs git itself
;   * on uninstall asks about the user data and keeps it by default; /DELETEUSERDATA=1 answers that
;     question without asking and deletes it - the switch exists so that path is reachable by an
;     automated run (verify-silent.ps1), which can never answer the question itself

; 产品名。默认就是正式产品 DSH-AB；测试构建（build\build.ps1 -TestProduct）是另一个产品
; DSH-ABtest：名字、默认安装目录、开始菜单条目、卸载登记项的 DisplayName 前缀和产物名全都带后缀，
; 所以它与正式安装在同一台机器上互不干扰。Go 侧的 appName 由 -ldflags -X 注入同一个值。
#ifndef TestProduct
  #define TestProduct 0
#endif
#if TestProduct
  #define AppName "DSH-ABtest"
  #define DefaultDir "{localappdata}\DSH-ABtest"
  ; 测试构建的 exe 由 build.ps1 单独构建：绝不复用 payload 里那份（它带着正式产品的名字），也绝不
  ; 覆盖 build\DSH_AB.exe 或 dist 里的正式产物。
  #define AppExeSource "..\build\DSH_ABtest.exe"
#else
  #define AppName "DSH-AB"
  #define DefaultDir "{localappdata}\DSH-AB"
  #define AppExeSource "..\payload\DSH_AB.exe"
#endif
; Two different versions, and the build always passes both:
;   DshabVersion - this program's own version; source of truth: src\dsh-ab\VERSION
;   DshVersion   - the dsh that this payload carries (upstream tag dsh-v<DshVersion>)
; The defaults only exist so the script still compiles when opened by hand; a real
; build (build\build.ps1) passes /DDshabVersion=... /DDshVersion=... Both are shown on
; the welcome page and on the finished page, and both appear in the output file name.
#ifndef DshabVersion
  #define DshabVersion "0.0.0"
#endif
#ifndef DshVersion
  #define DshVersion "unknown"
#endif
#define AppVersion DshabVersion
#define AppPublisher "DSH-AB"
#define AppExeName "DSH_AB.exe"
; 载荷根（build\mkpayload.ps1 组装出来的那棵树）：仓库里的 payload\。ISCC 读不了超过 MAX_PATH 的
; **源**路径，所以载荷内的相对路径必须短——这一条由 mkpayload.ps1 结尾的路径门禁保证（≤ 180 字符），
; 加上仓库路径后仍远低于 260。曾经把载荷 subst 到空闲盘符再 /DPayload=X:\ 传进来，那只是把源前缀
; 缩短，救不了载荷内部超长的相对路径，已删除。
#define Payload "..\payload"

[Setup]
; AppId is what Inno keys its own bookkeeping on: the uninstall log it may append to, and the
; Add/Remove key it would name {AppId}_is1. It is a compile-time constant and therefore cannot
; describe several coexisting installations - with the single shared GUID of the first releases the
; second installation took the first one's Add/Remove entry over, and uninstalling either copy
; deleted that entry for both. Two things follow:
;   * CreateUninstallRegKey=no below: this setup owns no Add/Remove entry at all. [Code] writes and
;     deletes one entry per installation instead, named {AppId}_<install tag>_is1.
;   * the GUID is a fresh one, so nothing Inno does to AppId-keyed state - including the
;     "Deleting uninstall key left over from previous non administrative install" cleanup it logs -
;     can ever match, or delete, the entry of an installation made by an earlier setup.
; The "per-installation identity" block in [Code] carries the same GUID; its braces are doubled here
; only because a lone { starts a constant in this section.
AppId={{6F1D2A74-3B58-4C9E-8A17-5E0C4D3B9A62}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
VersionInfoVersion={#AppVersion}
DefaultDirName={#DefaultDir}
DisableDirPage=no
; "Remember the previous installation directory" can only lead into the wall now: the directory a
; previous install used is an installation directory, i.e. a non-empty one, and a non-empty one is
; always refused. There is nothing it could usefully remember, so it is off.
UsePreviousAppDir=no
; Inno must not own the Add/Remove entry: there is exactly one AppId in a compiled setup, and one
; entry cannot describe several installations. [Code] writes and deletes one per installation
; (WriteUninstallEntry), which is also why the two UninstallDisplay* directives are gone - they only
; ever fed the entry Inno used to create here.
CreateUninstallRegKey=no
; The welcome page is shown on purpose: [Messages] below carries a Chinese
; WelcomeLabel1/WelcomeLabel2, and without this line Inno hides the page, so those
; two strings never reach the screen. Readiness is not affected either way:
; the empty-folder check runs on the directory page, whose NextButtonClick fires for an
; unattended install too (measured on Inno 6.7.3: a silent install runs InitializeSetup,
; InitializeWizard and NextButtonClick for every page).
DisableWelcomePage=no
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
AllowNoIcons=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
OutputDir=..\dist
; 产物名 DSH-AB-<dshab version>-<dsh version>-setup.exe（测试构建是
; DSH-ABtest-<dshab version>-<dsh version>-setup.exe，两个产物名不会互相覆盖）
OutputBaseFilename={#AppName}-{#DshabVersion}-{#DshVersion}-setup
SetupIconFile=..\src\dsh-ab\assets\dsh.ico
Compression=lzma2/normal
SolidCompression=yes
WizardStyle=modern
; Inno Setup 6.4+ defaults RedirectionGuard to on, which makes Setup launch every
; [Run] entry with the "enforce redirection trust" mitigation - and Windows hands
; that mitigation down the process tree. dsh builds its profile module fallback out
; of NTFS junctions, so a mitigated dsh child is refused when it follows its own
; junction: every @deepseek-ai package fails to resolve, dsh exits with
; ERR_MODULE_NOT_FOUND and the entry point reports a failed start. The failure
; only ever hits the launch the installer performs, which is why the first start
; after an install died while every later start worked.
RedirectionGuard=no
; No AppMutex on purpose: it is what refused to install while a DSH_AB.exe was running, i.e. it
; banned a second DSH-AB on the same machine. The mutex the program takes is per installation now
; (src\dsh-ab\main.go), so two installations can run side by side. Nothing here watches it.
; SetupMutex is a different thing and does not touch that rule: it serialises Setup against Setup
; (and against the uninstaller), so two installations can never be created in the same instant.
; Overlapping installs are what made each of them name its shortcut without seeing the other: the
; second file overwrote the first name and one copy ended up with no Start menu entry (2026-09-22,
; real machine). Installing while DSH-AB runs stays allowed - SetupMutex does not look at that.
SetupMutex=DSH-AB-Setup

; Chinese only, and Chinese by default: the wizard, the uninstaller and every
; built-in prompt come from this file. (Its two "currently running" entries are
; unreachable now: nothing here watches a mutex - see the AppMutex note above.)
; Inno Setup 6.7.3 ships no Chinese translation at all, so the community
; translation is vendored next to this script and the build stays offline-capable.
; The file is UTF-8 without a BOM as upstream ships it; 6.7 auto-detects that
; (verified: compiling the same script with the same file re-encoded as CP936
; produces a byte-identical setup.exe). Everything user-visible outside this file
; (the port page, the empty-folder refusal, the uninstall prompt) is written in
; Chinese further down, in [Messages] and [Code].
[Languages]
Name: "chinesesimplified"; MessagesFile: "languages\ChineseSimplified.isl"

[Tasks]
; unchecked on purpose, as it has always been: the desktop shortcut is opt-in. It is still per
; installation like the Start Menu one, so a silent test install that wants to see it has to pass
; /TASKS=desktopicon - without that flag no desktop shortcut is created at all.
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
; 正式构建来自 payload（mkpayload.ps1 放进去的 build\DSH_AB.exe），测试构建来自 build.ps1 单独构建
; 的那一份：见上面的 AppExeSource。
; DestName: 测试构建的源文件叫 DSH_ABtest.exe（build.ps1 单独构建的那份），但装进 {app} 的名字必须是
; AppExeName（DSH_AB.exe）：图标、[Run] 和 naming.go 的扫描规则都认这个名字。
Source: "{#AppExeSource}"; DestDir: "{app}"; DestName: "{#AppExeName}"; Flags: ignoreversion
Source: "{#Payload}\dsh-ab.toml"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\LICENSES.txt"; DestDir: "{app}"; Flags: ignoreversion
; The two ledgers the AI maintains; the installer bakes the installation path into them.
Source: "{#Payload}\docs\*"; DestDir: "{app}\docs"; Flags: ignoreversion
; Named gitignore.template in the templates directory, installed under its real name.
Source: "{#Payload}\.gitignore"; DestDir: "{app}"; DestName: ".gitignore"; Flags: ignoreversion
; slot-a 的 data\ 是这个槽的用户数据（dsh 的 DSH_HOME：会话、设置、以及随包安装的那份 skill）。
; 卸载时会问「要同时删除用户数据吗」，答「否」（默认）就必须把它整份留下 —— 所以它单独一条、
; 带 uninsneveruninstall：Inno 只删 [Files] 里装过、又没标这个标志的文件，而数据里恰恰有随包 skill
; 是安装器放进去的（2026-09-23 实测：不带这个标志时，卸载后 slot-a\data 由 8 个文件变成 7 个，
; 少的就是 data\skills\<skill>\SKILL.md，用户数据并没有被原样保留）。
; 另外：[Code] 的 RemoveAll 不看这个标志，「删用户数据」那一路照样用 DelTree 删掉整个槽。
; Excludes 里这个反斜杠不能少：不带反斜杠的模式匹配的是**任意层级**同名的那一项，于是
; slot-a\app\node_modules\@earendil-works\pi-ai\dist\providers\data\*.json(40 个、583 KB，
; all.js 与各 <provider>.models.js 都静态 import 它们）与 node\...\node-gyp\gyp\data\{win,ninja}
; 会被一起排掉：装出来的 dsh 一 import 就失败，而安装包照旧编得出来。实测 45 个文件静默没进安装根。
; "\data" 是「相对本行 Source 的顶层 data」，也就是 slot-a\data，正是要排除的那一个。
Source: "{#Payload}\slot-a\*"; DestDir: "{app}\slot-a"; Excludes: "\data"; Flags: ignoreversion recursesubdirs createallsubdirs
; 这一条与上一条声明的是同一批目标路径，而 Inno 对同一目标路径只留一条、**首条胜出**：上一条把
; 顶层 data 排掉之后，slot-a\data 下每个文件的落点就只剩这一条声明，uninsneveruninstall 真正生效，
; 「卸载保留用户数据」才留得住随包那份 skill。
Source: "{#Payload}\slot-a\data\*"; DestDir: "{app}\slot-a\data"; Flags: ignoreversion recursesubdirs createallsubdirs uninsneveruninstall
Source: "{#Payload}\runtime\*"; DestDir: "{app}\runtime"; Flags: ignoreversion recursesubdirs createallsubdirs

[Dirs]
Name: "{app}\state"
Name: "{app}\logs"
; Empty on purpose. An empty slot still counts as "not installed": slotInstalled() requires
; node\node.exe and the dsh entry together, so the tray refuses to switch to it.
Name: "{app}\slot-b"

[Icons]
; 名字与位置都由 [Code] 算出来（InstallIconPath / DesktopIconPath 只给叶子名，根由
; {userprograms} / {autodesktop} 给出）：任何时候都是 <Programs>\<名字>.lnk，没有子文件夹。不再用
; {group}，因为 DefaultGroupName 会把安装塞进 DSH-AB 子文件夹。重名的保险写在 InstallName 里：
; 名字真的撞上时补本安装的 tag，后装的那份不会覆盖先装的。
; Name 必须以常量打头：Inno 在编译期就要求这一行带路径（"must include a path for the icon"），
; 一个光秃秃的 {code:...} 直接编译不过（上一轮没编译过，就是这么漏过去的）。
Name: "{userprograms}\{code:InstallIconPath}"; Filename: "{app}\{#AppExeName}"; WorkingDir: "{app}"
Name: "{autodesktop}\{code:DesktopIconPath}"; Filename: "{app}\{#AppExeName}"; WorkingDir: "{app}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExeName}"; Description: "{cm:LaunchProgram,{#AppName}}"; Flags: nowait postinstall skipifsilent

[Messages]
WelcomeLabel1=欢迎安装 [name]
WelcomeLabel2=安装程序会把 [name/ver] 装进一个空文件夹。%n%n这个程序只做入口、切槽、托盘和安装/卸载，它不会修改任何 dsh 文件。%n%n这个安装包里内置的 dsh 版本：{#DshVersion}
SelectDirLabel3=安装程序会把 [name] 装进下面这个文件夹。这个文件夹必须是空的。
SelectDirBrowseLabel=单击“下一步”继续。想换一个文件夹就单击“浏览”。
DiskSpaceMBLabel=至少需要 [mb] MB 的磁盘空间。
ReadyLabel1=安装程序已经准备好，可以开始安装 [name]。
ReadyLabel2a=单击“安装”开始安装，或者单击“上一步”检查或修改设置。
ButtonBack=< 上一步(&B)
ButtonNext=下一步(&N) > 
ButtonInstall=安装(&I)
ButtonFinish=完成(&F)
ButtonCancel=取消
ButtonBrowse=浏览(&R)...
ButtonYes=是(&Y)
ButtonNo=否(&N)
; The finished page names the built-in dsh version too. The wording of
; FinishedLabel/FinishedLabelNoIcons is the language file's own (no sentence is
; repeated, and ClickFinish still ends the page); only the version line is added, and
; both variants carry it because an installation without any icon uses the NoIcons one.
FinishedLabel=安装程序已在您的计算机中安装了 [name]。您可以通过已安装的快捷方式运行此应用程序。%n%n这个安装包里内置的 dsh 版本：{#DshVersion}
FinishedLabelNoIcons=安装程序已在您的计算机中安装了 [name]。%n%n这个安装包里内置的 dsh 版本：{#DshVersion}
ConfirmUninstall=确定要卸载 %1 及其所有组件吗？

[Code]
var
  PortsPage: TInputQueryWizardPage;
  ProductionPort: Integer;
  DeleteUserData: Boolean;
  // 名字和两条快捷方式的落点只算一次，三个读取方都从这里拿（见 ComputeNames）。
  NameComputed: Boolean;
  CachedName, CachedIconPath, CachedDesktopIconPath: String;

function DirIsEmpty(const Dir: string): Boolean;
var
  FindRec: TFindRec;
begin
  Result := True;
  if not DirExists(Dir) then
    Exit;
  if FindFirst(AddBackslash(Dir) + '*', FindRec) then
  begin
    try
      repeat
        if (FindRec.Name <> '.') and (FindRec.Name <> '..') then
        begin
          Result := False;
          Break;
        end;
      until not FindNext(FindRec);
    finally
      FindClose(FindRec);
    end;
  end;
end;

function TryParsePort(const S: string; var Port: Integer): Boolean;
var
  V: Integer;
begin
  V := StrToIntDef(Trim(S), -1);
  Result := (V >= 1) and (V <= 65535);
  if Result then
    Port := V;
end;

// PortError says why the install cannot go on and then stops it. SuppressibleMsgBox, not MsgBox:
// a script MsgBox ignores /SUPPRESSMSGBOXES and would block an unattended
// install forever, so the log line is what a silent caller actually gets to read.
procedure PortError(const Message: string);
begin
  Log('DSH-AB: ' + Message);
  SuppressibleMsgBox(Message, mbError, MB_OK, IDOK);
end;

// ---- port availability -----------------------------------------------------------------------
// dsh binds 127.0.0.1:<production port>, so the honest test for "is this port taken" is a bind on
// that same address: it catches a listener *and* a port that is reserved without anyone listening
// (a client socket, an excluded port range). connect() would only see listeners, and parsing
// `netstat -ano -p tcp` would have to read the state column, which is localized ("LISTENING" on an
// English Windows, 中文 elsewhere) and therefore drifts.
//
// 2026-09-22（审计 2）：探测走 powershell.exe —— 与 OwnRunningProcesses 同一条系统进程途径，不再自己
// 声明一套 ws2_32 调用，也不新增任何外部二进制。子进程里真的执行 Socket.Bind(127.0.0.1:Port)：退出码
// 0 = 绑定成功 = 端口可用，其它 = 不可用（绑定失败、PowerShell 起不来、被策略拦住）。判据只有退出码，
// **不解析任何文本**，所以与系统语言无关。
// 绑定用的是朴素 bind：既没有 SO_REUSEADDR，也没有 SO_EXCLUSIVEADDRUSE。Windows 上只有自己设了
// SO_REUSEADDR 的 socket 才能抢到别人已占的端口，所以朴素 bind 一定 WSAEADDRINUSE（Winsock 的绑定表）；
// 而 SO_EXCLUSIVEADDRUSE 会把仅仅处于 TIME_WAIT 的端口也算成占用，那不是用户说的「被占用」。
//
// PortProbe is the one-liner that really binds 127.0.0.1:Port inside that child and exits 0 on
// success. Only single quotes appear in it: the whole script travels inside the double quotes of
// -Command, and Inno does not escape anything for us.
function PortProbe(Port: Integer): String;
begin
  Result :=
    '$s=New-Object System.Net.Sockets.Socket -ArgumentList' +
    ' ([System.Net.Sockets.AddressFamily]::InterNetwork),' +
    '([System.Net.Sockets.SocketType]::Stream),' +
    '([System.Net.Sockets.ProtocolType]::Tcp);' +
    ' try { $s.Bind((New-Object System.Net.IPEndPoint -ArgumentList' +
    ' ([System.Net.IPAddress]::Parse(''127.0.0.1''), ' + IntToStr(Port) + ')));' +
    ' $s.Close(); exit 0 }' +
    ' catch { $s.Close(); exit 1 }';
end;

// IsPortFree reports whether 127.0.0.1:Port can still be bound. Any failure to even ask (no
// PowerShell, a policy block) counts as "not free": refusing is the safe side, and the log line
// says which step failed.
function IsPortFree(Port: Integer): Boolean;
var
  ResultCode: Integer;
begin
  if not Exec('powershell.exe',
              '-NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "' +
              PortProbe(Port) + '"',
              '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    Log('DSH-AB: cannot run powershell.exe, port ' + IntToStr(Port) + ' cannot be checked');
    Result := False;
    Exit;
  end;
  Result := ResultCode = 0;
  if not Result then
    Log('DSH-AB: bind 127.0.0.1:' + IntToStr(Port) + ' failed (exit ' +
        IntToStr(ResultCode) + '), that port is not usable');
end;

// PortIsFree checks the one port this installer asks for and, when it is taken, names it together
// with the value in force, so the message can say exactly which number to change.
function PortIsFree(Production: Integer; var Reason: string): Boolean;
begin
  Result := IsPortFree(Production);
  if not Result then
    Reason := '当前填写的生产端口 ' + IntToStr(Production) + ' 已被占用（绑定 127.0.0.1:' +
              IntToStr(Production) + ' 失败）。请先腾出这个端口，或者用 /PRODUCTION=<其它端口> 重新安装。';
end;

// FreePort searches upwards from From for the first port that is free on this machine and reports
// whether it found one (B3a). Upwards only, because wrapping around the range would hand out a port
// nobody expects.
function FreePort(From: Integer; var Port: Integer): Boolean;
var
  P: Integer;
begin
  Result := False;
  P := From;
  while P <= 65535 do
  begin
    if IsPortFree(P) then
    begin
      Port := P;
      Result := True;
      Exit;
    end;
    P := P + 1;
  end;
  Log('DSH-AB: no free port found from ' + IntToStr(From) + ' upwards');
end;

// FindCmdLineValue returns the value of the /Name=Value setup parameter, or '' when it is absent.
// Same shape as the switches Setup itself takes (/DIR=, /LOG=), so the port reads like the rest of
// the command line: /PRODUCTION=3090.
function FindCmdLineValue(const Name: string): string;
var
  I, P: Integer;
  S: string;
begin
  Result := '';
  for I := 1 to ParamCount do
  begin
    S := ParamStr(I);
    P := Pos('=', S);
    if (P < 2) or (Copy(S, 1, 1) <> '/') then
      Continue;
    if CompareText(Copy(S, 2, P - 2), Name) = 0 then
    begin
      Result := Copy(S, P + 1, Length(S));
      Exit;
    end;
  end;
end;

// InitializeSetup runs before the wizard on both paths, so it is the one place that always sees the
// port. Measured on Inno 6.7.3, a silent install also runs InitializeWizard and every page's
// NextButtonClick, so the ports page check fires there as well; that is why the expensive port probe
// below is guarded by WizardSilent and the page keeps the user's own value authoritative in the
// wizard. The command line is read here, once, for both paths: the page is seeded from this
// variable, so the wizard and an unattended install have one source of truth
// (/PRODUCTION, then the default 3090).
// A value that cannot be used aborts the install rather than silently falling back to a default: an
// install on the wrong port is worse than no install.
function InitializeSetup: Boolean;
var
  S: string;
  P1: Integer;
  Reason: string;
begin
  DeleteUserData := False;
  ProductionPort := 3090;

  S := FindCmdLineValue('PRODUCTION');
  if S <> '' then
  begin
    if not TryParsePort(S, P1) then
    begin
      PortError('生产端口 /PRODUCTION=' + S + ' 无效：必须是 1 到 65535 之间的整数。');
      Result := False;
      Exit;
    end;
    ProductionPort := P1;
  end;

  // Whatever the command line and the defaults decided is what lands in dsh-ab.toml, so a port that
  // is already taken has to stop the install here. In the wizard the same check also runs when the
  // user leaves the ports page (NextButtonClick), where a taken value is still changeable.
  if WizardSilent and (not PortIsFree(ProductionPort, Reason)) then
  begin
    PortError(Reason);
    Result := False;
    Exit;
  end;

  Result := True;
end;

// InitializeWizard builds the ports page. The value on it comes from the command line or the default
// (InitializeSetup) and is only *seeded* here: what the user types still wins, and NextButtonClick
// checks it again before it becomes ProductionPort.
procedure InitializeWizard;
var
  Sub: string;
  SeedProd: Integer;
  Reason: string;
begin
  SeedProd := ProductionPort;
  Sub := '生产端口是活动槽对外的端口，下面这个值会写进 dsh-ab.toml，' + #13#10 +
         '以后也可以直接改那个文件（改完要重启 DSH_AB.exe）。';

  // B3a: every installation defaults to the same port, so a user whose 3090 is already taken - by
  // another DSH-AB, which is a supported setup - would otherwise fill in the page and be refused on
  // the way out of it. The wizard therefore *offers* the first free port above the seeded one and
  // says so on the page. It is behind "not WizardSilent" on purpose: an unattended install must keep
  // failing loudly on an occupied port (InitializeSetup) and must never switch ports behind the
  // caller's back, which is what verify-silent.ps1 scenario 3 pins.
  if (not WizardSilent) and
     (not PortIsFree(SeedProd, Reason)) and
     FreePort(SeedProd, SeedProd) then
  begin
    Sub := Sub + #13#10 + #13#10 +
           '默认端口已被占用，已改为 ' + IntToStr(SeedProd) + '。' + #13#10 +
           '不想用这一个，就把下面的框改成你要的端口。';
  end;

  // The page's own text is built before it exists, because Inno exposes no writable sub caption
  // afterwards (only Caption and Description can still be changed).
  PortsPage := CreateInputQueryPage(wpSelectDir,
    '端口设置',
    'DSH-AB 用哪个端口对外服务？',
    Sub);
  PortsPage.Add('生产端口 production：', False);
  // Seeded from InitializeSetup, never from a literal: the page used to carry its own '3090', which
  // is exactly why a /PRODUCTION= command line could not reach a page-less (silent) install at all.
  // What the user types here still wins over the command line.
  PortsPage.Values[0] := IntToStr(SeedProd);
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  P1: Integer;
  Reason: string;
begin
  Result := True;

  if CurPageID = wpSelectDir then
  begin
    if not DirIsEmpty(WizardDirValue) then
    begin
      // SuppressibleMsgBox, not MsgBox: a script MsgBox ignores
      // /SUPPRESSMSGBOXES and would block a silent install forever on a non-empty directory.
      Log('DSH-AB: refusing to install, target directory is not empty: ' + WizardDirValue);
      SuppressibleMsgBox('DSH-AB 只能装进一个空文件夹。' + #13#10 + #13#10 +
             WizardDirValue + #13#10 + #13#10 +
             '这个文件夹里已经有东西了。请换一个空文件夹，或者先把它清空。' + #13#10 +
             '安装程序不会覆盖、也不会合并任何已存在的文件。',
             mbError, MB_OK, IDOK);
      Result := False;
      Exit;
    end;
  end
  else if (PortsPage <> nil) and (CurPageID = PortsPage.ID) then
  begin
    if not TryParsePort(PortsPage.Values[0], P1) then
    begin
      SuppressibleMsgBox('生产端口必须是 1 到 65535 之间的整数。', mbError, MB_OK, IDOK);
      Result := False;
      Exit;
    end;
    // A port that is already taken would make the installed instance unusable, so it is refused
    // here too - the user is still on the page and can type another one.
    if not PortIsFree(P1, Reason) then
    begin
      SuppressibleMsgBox(Reason, mbError, MB_OK, IDOK);
      Result := False;
      Exit;
    end;
    ProductionPort := P1;
  end;
end;

// ReplaceLineValue puts Value on the line that starts with Key, and leaves every other byte
// of the file alone.
//
// The edit works on raw bytes on purpose. Inno has no UTF-8 file helper (6.7 has no
// *FileUTF8 functions), and SaveStringsToFile re-encodes the whole file to the ANSI code page
// — which silently turns the UTF-8 comments of dsh-ab.toml into mojibake. Both the key and the
// replacement here are pure ASCII, and UTF-8 never stores an ASCII byte inside a multi-byte
// sequence, so a byte-wise match cannot cut a character in half.
function ReplaceLineValue(const Raw: AnsiString; const Key: AnsiString; const Value: AnsiString): AnsiString;
var
  P, E: Integer;
begin
  Result := Raw;
  P := Pos(Key, Result);
  if P = 0 then
  begin
    Log('DSH-AB: line "' + Key + '" not found, leaving it as it is');
    Exit;
  end;

  // Walk to the end of that line and splice the value in.
  E := P + Length(Key);
  while (E <= Length(Result)) and (Result[E] <> #10) and (Result[E] <> #13) do
    E := E + 1;

  Result := Copy(Result, 1, P + Length(Key) - 1) + Value + Copy(Result, E, Length(Result) - E + 1);
end;

procedure WritePorts;
var
  Path: string;
  Raw: AnsiString;
  Prod: AnsiString;
begin
  if ProductionPort < 1 then
  begin
    Log('DSH-AB: no ports were chosen, keeping the defaults in dsh-ab.toml');
    Exit;
  end;

  Path := ExpandConstant('{app}\dsh-ab.toml');
  if not LoadStringFromFile(Path, Raw) then
  begin
    Log('DSH-AB: cannot read ' + Path + ', keeping the default ports');
    Exit;
  end;

  Prod := IntToStr(ProductionPort);
  Raw := ReplaceLineValue(Raw, 'production = ', Prod);

  if not SaveStringToFile(Path, Raw, False) then
    Log('DSH-AB: cannot write ' + Path + ', keeping the default ports');
end;

// ReplaceAllByte replaces every occurrence of Needle, and works on raw bytes for the same reason
// ReplaceLineValue does (see above): the shipped files are UTF-8, Inno has no UTF-8 file helper,
// and both the placeholder and the replacement are pure ASCII, so a byte-wise match cannot cut a
// character in half. The replacement never contains the placeholder, so this loop terminates.
// (No parameter may be called To/New: those are Pascal keywords.)
function ReplaceAllByte(const Raw: AnsiString; const Needle: AnsiString;
                        const Replacement: AnsiString): AnsiString;
var
  P: Integer;
begin
  Result := Raw;
  P := Pos(Needle, Result);
  while P > 0 do
  begin
    Result := Copy(Result, 1, P - 1) + Replacement +
              Copy(Result, P + Length(Needle), Length(Result));
    P := Pos(Needle, Result);
  end;
end;

// BakeInstallPath writes the real absolute installation path into a shipped file, replacing the
// {{DSH_AB_ROOT}} placeholder the payload was built with. The AI is told the path in one
// authoritative place instead of guessing it. Every failure is logged and the install goes on: the
// file then keeps the placeholder, and the log says so.
procedure BakeInstallPath(const RelPath: string);
var
  Path: string;
  Raw: AnsiString;
begin
  Path := ExpandConstant('{app}\' + RelPath);
  if not FileExists(Path) then
  begin
    Log('DSH-AB: cannot bake the installation path, file is missing: ' + Path);
    Exit;
  end;
  if not LoadStringFromFile(Path, Raw) then
  begin
    Log('DSH-AB: cannot read ' + Path + ', the placeholder stays');
    Exit;
  end;
  if Pos('{{DSH_AB_ROOT}}', Raw) = 0 then
  begin
    Log('DSH-AB: no {{DSH_AB_ROOT}} placeholder in ' + Path + ', nothing to bake');
    Exit;
  end;

  Raw := ReplaceAllByte(Raw, '{{DSH_AB_ROOT}}', ExpandConstant('{app}'));
  // Any {{...}} left over is a placeholder this script does not know: say so instead of shipping
  // it silently into a document the AI is supposed to follow.
  if Pos('{{', Raw) > 0 then
    Log('DSH-AB: WARNING ' + Path + ' still carries a {{...}} placeholder after baking');
  if not SaveStringToFile(Path, Raw, False) then
    Log('DSH-AB: cannot write ' + Path + ', the placeholder stays');
end;

// The three shipped files that carry a path the AI has to know: the maintenance skill and the two
// ledgers.
procedure BakeInstallPaths;
begin
  BakeInstallPath('slot-a\data\skills\dsh-self-maintenance\SKILL.md');
  BakeInstallPath('docs\PLUGINS.md');
  BakeInstallPath('docs\TODO.md');
end;

// ---- per-installation identity -----------------------------------------------------------------
// AppId is a compile-time constant, so it cannot tell two coexisting installations apart: Inno would
// derive one Add/Remove key ({AppId}_is1) and one set of shortcut names for all of them, the second
// install would take both over, and uninstalling either copy would delete them for the other
// Every per-installation value is derived from the
// installation directory instead, and every one of them is derived the same way at install time and
// at uninstall time - that is what lets an uninstaller delete exactly the entry and the shortcuts of
// the installation it belongs to. One derived value, the installer's internal identity:
//   InstallTag     the disambiguator: a hash of the installation directory, and the last resort of
//                  InstallName below when no port can tell two installations apart
// The directory never changes during an installation's life, so the tag is stable. Twelve hex
// characters of SHA-256 are plenty here: the tag only has to be unique on one machine, and two
// installations never share it unless they are the same directory. LowerCase plus
// RemoveBackslashUnlessRoot only keep the tag stable when the very same directory is spelled
// differently (D:\DSH-AB vs d:\dsh-ab\), which is cheap insurance for the uninstaller's sake.
function InstallTag: String;
begin
  Result := Copy(GetSHA256OfUnicodeString(
    LowerCase(RemoveBackslashUnlessRoot(ExpandConstant('{app}')))), 1, 12);
end;

// ---- the name this installation shows and where its shortcut lives ----------------------------
// 名字规则（与 src\dsh-ab\naming.go 的 installName 同一套）：名字里不出现安装目录的名字，也不看默认目录。
//   * 本机只有这一份，且端口可用           → DSH-AB
//   * 本机有别的份，本份端口可用且不冲突   → DSH-AB (<ports.production>)
//   * 端口不可用，或与别的份的端口相同     → DSH-AB (<tag>)（tag = 安装根 SHA-256 前 12 位，与卸载键同源）
// 括号属于名字本身：各显示面直接用这个名字，谁都不再补括号。
// 「冲突」= 本份的 ports.production 与另一份安装的生产端口相同。这一条是必须的：
// 共存的两份的快捷方式都落在同一个 DSH-AB 文件夹，名字若相同，后装的那份会覆盖先装的那份的快捷方式，
// 而「绝不替别的安装改快捷方式」不允许这种事。
//
// 这里算的是安装那一刻的名字。DSH-AB 启动时用同一套规则再算一次并重写自己的条目与快捷方式，所以
// 以后改 toml 里的端口，名字会跟着变（启动时对齐）；名字真的退化到 tag 时，那一次启动还会
// 弹窗请用户改端口。src\dsh-ab\naming.go 的 installName 是同一个函数，两处必须逐字同规则。
// AddLine appends one line to a CRLF-separated list; '' is the empty list. The uninstaller's
// leftover list and OtherInstallDirs below both use it (Inno has no forward references, so the one
// copy has to sit above its first caller).
function AddLine(const List, Line: string): string;
begin
  if List = '' then
    Result := Line
  else
    Result := List + #13#10 + Line;
end;

// IsOwnProductEntry 是「一条卸载条目算不算本产品」的扫描口径，必须与 src\dsh-ab\naming.go 的 isDshAbEntry 逐字同规则：
// DisplayName 的第一个词就是产品名（正式安装 DSH-AB，测试构建 DSH-ABtest），Inno 在它后面接自己的
// 「<版本> (<安装名>)」。只要那个词以家族名 DSH-AB 打头，这条条目就属于那个产品——另一个产品不许用
// exe 兜底把它认领过去，否则正式安装与测试构建会互相改名、互相把对方算成「另一份安装」（两边装的是
// 同一个 DSH_AB.exe，凭 exe 名根本分不开）。DisplayName 完全没有产品名时，才用 exe 兜底认领（旧版本
// 装出来的那一条走的就是这条路，它的 AppId 是另一份 GUID）。
function IsOwnProductEntry(const Dn, Us, Di: String): Boolean;
var
  W: String;
  P: Integer;
begin
  Result := False;
  W := Trim(Dn);
  if W <> '' then
  begin
    P := Pos(' ', W);
    if P > 0 then
      W := Copy(W, 1, P - 1);
    if Pos('DSH-AB', W) = 1 then
    begin
      Result := CompareText(W, '{#AppName}') = 0;
      Exit;
    end;
  end;
  Result := (Pos('DSH_AB.exe', Us) > 0) or (Pos('DSH_AB.exe', Di) > 0);
end;

// OtherInstallDirs lists the installation directories of every OTHER installation of *this* product
// on the machine, read from the HKCU Add/Remove entries. CRLF-separated, '' when this is the only
// one. 目录已经不存在的条目仍然丢掉：一条过期条目不该把独苗安装推进共用的子文件夹（Go 那边同样
// 过滤）。
function OtherInstallDirs: String;
var
  Names: TArrayOfString;
  I: Integer;
  Key, Root, Dn, Us, Di: String;
begin
  Result := '';
  if not RegGetSubkeyNames(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Uninstall', Names) then
    Exit;
  for I := 0 to GetArrayLength(Names) - 1 do
  begin
    Key := 'Software\Microsoft\Windows\CurrentVersion\Uninstall\' + Names[I];
    if not RegQueryStringValue(HKCU, Key, 'InstallLocation', Root) then
      Continue;
    // A missing value must read as an empty string, not as the previous entry's text.
    Dn := ''; Us := ''; Di := '';
    RegQueryStringValue(HKCU, Key, 'DisplayName', Dn);
    RegQueryStringValue(HKCU, Key, 'UninstallString', Us);
    RegQueryStringValue(HKCU, Key, 'DisplayIcon', Di);
    if not IsOwnProductEntry(Dn, Us, Di) then
      Continue;
    Root := RemoveBackslashUnlessRoot(Root);
    if (Root = '') or (not DirExists(Root)) then
      Continue;
    if CompareText(Root, RemoveBackslashUnlessRoot(ExpandConstant('{app}'))) = 0 then
      Continue;
    Result := AddLine(Result, Root);
  end;
end;

// PortsOfInstall reads ports.production out of another installation's dsh-ab.toml. It looks for the
// line whose key is "production" - that key exists only under [ports] in the shipped file - and
// leaves 0 in a variable whose key is missing or unusable. 0, not -1: this value is only ever
// compared against this installation's own port, and src\dsh-ab\naming.go reads a missing config as
// 0 too (a Go int's zero value). Two different answers here would be two different names for one
// installation.
procedure PortsOfInstall(const Dir: String; var Prod: Integer);
var
  Lines: TArrayOfString;
  I, P: Integer;
  S, K: String;
begin
  Prod := 0;
  if not LoadStringsFromFile(AddBackslash(Dir) + 'dsh-ab.toml', Lines) then
    Exit;
  for I := 0 to GetArrayLength(Lines) - 1 do
  begin
    S := Trim(Lines[I]);
    if (S = '') or (S[1] = '#') then
      Continue;
    P := Pos('=', S);
    if P < 2 then
      Continue;
    K := Trim(Copy(S, 1, P - 1));
    if CompareText(K, 'production') = 0 then
      Prod := StrToIntDef(Trim(Copy(S, P + 1, Length(S))), 0);
  end;
end;

// PortsConflict is the second half of the fallback: this installation and another one serve the same
// production port, so neither can tell the other apart in the 开始菜单 folder - that folder has no
// subfolders, so the names alone are what separates the two installations.
// 只看生产端口：那是这份安装真正的身份（第二个槽在 AI 自己挑的端口上验证，那个端口从不属于 DSH-AB）。
// Dirs is the CRLF list OtherInstallDirs returns.
function PortsConflict(const Dirs: String; Prod: Integer): Boolean;
var
  Rest, Peer: String;
  P, PeerProd: Integer;
begin
  Result := False;
  Rest := Dirs;
  while Rest <> '' do
  begin
    P := Pos(#13#10, Rest);
    if P = 0 then
    begin
      Peer := Rest;
      Rest := '';
    end
    else
    begin
      Peer := Copy(Rest, 1, P - 1);
      Rest := Copy(Rest, P + 2, Length(Rest));
    end;
    PortsOfInstall(Peer, PeerProd);
    if PeerProd = Prod then
    begin
      Result := True;
      Exit;
    end;
  end;
end;

// InstallName is what 应用和功能、快捷方式和状态弹窗标题显示的名字, and the rule table is:
// the only DSH-AB on the
// machine is plain DSH-AB, one that coexists with another carries its production port in
// parentheses, and one whose port is unusable or collides with another installation's falls back to
// the tag in parentheses. 括号属于名字本身, so every face shows this string verbatim and none of
// them adds brackets of its own. The installation directory's own name deliberately does not enter
// it. It is a scripted constant ({code:InstallName}), hence the Param the callers do not pass;
// src\dsh-ab\naming.go's installName is the same function and the two must answer the same thing
// for the same machine, so any change here lands there too.
// ComputeNames answers all three questions in one look at the machine and remembers the answers.
// [Icons] asks for the shortcut path, WriteUninstallEntry asks for the name and the two paths; while
// each of them scanned the registry again, a second installation appearing in between changed
// OtherInstallDirs and the two answers disagreed - the entry then named a file the installer never
// created, and that copy ended up with no Start menu entry at all (2026-09-22, real machine: the
// entry said <Programs>\DSH-ABtest\DSH-ABtest (3190).lnk while the install had created the bare
// one). [Setup] SetupMutex keeps two setups from overlapping; this keeps the three readers agreeing.
procedure ComputeNames;
var
  Dirs: String;
begin
  Dirs := OtherInstallDirs;
  if (not IsPortFree(ProductionPort)) or PortsConflict(Dirs, ProductionPort) then
    CachedName := '{#AppName} (' + InstallTag + ')'
  else if Dirs = '' then
    CachedName := '{#AppName}'
  else
    CachedName := '{#AppName} (' + IntToStr(ProductionPort) + ')';
  // P3：落点永远就是 <Programs>\<名字>.lnk，不再有 <Programs>\{#AppName}\ 子文件夹。
  // 这台机器的外壳只要看到 .lnk 被改动过就不再按文件夹分组，显示成散的、和磁盘不一致，重启外壳才恢复；
  // 直接放根下，磁盘上是什么、用户就看到什么。名字本身照旧（它已经把各份区分开了），只有目录这一维没了。
  CachedIconPath := CachedName;
  CachedDesktopIconPath := CachedName;
  NameComputed := True;
end;

function InstallName(Param: String): String;
begin
  if not NameComputed then
    ComputeNames;
  Result := CachedName;
end;

// 两个快捷方式落点只给叶子名：目录由 [Icons] 的 Name 前缀 {userprograms} /
// {autodesktop} 给出，因为 Inno 在编译期就要求 Name 那一行带路径，光一个 {code:...} 编译不过。
function InstallIconPath(Param: String): String;
begin
  if not NameComputed then
    ComputeNames;
  Result := CachedIconPath;
end;

function DesktopIconPath(Param: String): String;
begin
  if not NameComputed then
    ComputeNames;
  Result := CachedDesktopIconPath;
end;

// This installation's Add/Remove key: Inno's own key name for one installation, with the tag spliced
// in, so that no two installations can ever resolve to the same key. The GUID is [Setup] AppId
// verbatim - its braces are doubled there only because a lone { starts a constant in that section.
// The name is built, never stored, because {app} is known in both directions: from the wizard (or
// /DIR=) during the install, and from the uninstall log during the uninstall.
function UninstallRegPath: String;
begin
  Result := 'Software\Microsoft\Windows\CurrentVersion\Uninstall\' +
            '{6F1D2A74-3B58-4C9E-8A17-5E0C4D3B9A62}_' + InstallTag + '_is1';
end;

// DirSizeBytes sums the size of every file below Dir. Recursive because the payload is: slot-a
// carries a whole node_modules tree. Called once, at the end of the install, over the bytes that are
// really on disk - there is no shorter way to answer "how big is this installation", Windows itself
// computes nothing and stores nothing.
function DirSizeBytes(const Dir: string): Cardinal;
var
  FindRec: TFindRec;
  Full: string;
begin
  Result := 0;
  if not FindFirst(AddBackslash(Dir) + '*', FindRec) then
    Exit;
  try
    repeat
      if (FindRec.Name = '.') or (FindRec.Name = '..') then
        Continue;
      Full := AddBackslash(Dir) + FindRec.Name;
      if (FindRec.Attributes and $10) <> 0 then
        Result := Result + DirSizeBytes(Full)
      else
        Result := Result + (Int64(FindRec.SizeHigh) shl 32) + Int64(FindRec.SizeLow);
    until not FindNext(FindRec);
  finally
    FindClose(FindRec);
  end;
end;

// WriteUninstallEntry is the Add/Remove entry of *this* installation. Inno's own is off
// (CreateUninstallRegKey=no in [Setup]), because one AppId cannot describe several installations.
// The value names are the ones Inno writes for a per-user installation, and DisplayName carries
// InstallName, so 应用和功能 lists one clearly named entry per installation. It runs at
// ssPostInstall - where Inno used to write its own entry, i.e. once the files are in place, so a
// refused or cancelled install never leaves an entry behind. PrivilegesRequired=lowest makes this a
// per-user entry in HKCU, which is where the installation root and the uninstaller both live.
procedure WriteUninstallEntry;
var
  Root, Key: String;
begin
  Root := ExpandConstant('{app}');
  Key := UninstallRegPath;
  // DisplayName 就是名字本身：不带版本号（版本在 DisplayVersion，系统自己有版本列），也不额外补
  // 括号（括号已经在 InstallName 的返回值里）。
  RegWriteStringValue(HKCU, Key, 'DisplayName', InstallName(''));
  RegWriteStringValue(HKCU, Key, 'DisplayVersion', '{#AppVersion}');
  RegWriteStringValue(HKCU, Key, 'Publisher', '{#AppPublisher}');
  RegWriteStringValue(HKCU, Key, 'InstallLocation', Root + '\');
  RegWriteStringValue(HKCU, Key, 'UninstallString', '"' + Root + '\unins000.exe"');
  RegWriteStringValue(HKCU, Key, 'QuietUninstallString', '"' + Root + '\unins000.exe" /SILENT');
  RegWriteStringValue(HKCU, Key, 'DisplayIcon', Root + '\{#AppExeName}');
  // 两条快捷方式的落点，交给 DSH-AB 自己维护：它靠这两个值把自己那一条改名，并把旧版留在
  // <Programs>\{#AppName}\ 里的那一条搬出来。不能靠读 .lnk 认领——
  // 外壳把目标路径拆成 shell item 与相对的 LinkInfo 路径，绝对路径在文件里根本不连续（实测），
  // 猜错就会动到别的安装的快捷方式。桌面那条在没勾桌面任务时并不存在，DSH-AB 会自己跳过。
  RegWriteStringValue(HKCU, Key, 'DshAbStartMenuLink',
    ExpandConstant('{userprograms}\') + InstallIconPath('') + '.lnk');
  RegWriteStringValue(HKCU, Key, 'DshAbDesktopLink',
    ExpandConstant('{autodesktop}\') + DesktopIconPath('') + '.lnk');
  RegWriteStringValue(HKCU, Key, 'InstallDate', GetDateTimeString('yyyymmdd', ' ', ' '));
  // EstimatedSize is the "大小" column of 应用和功能 (a DWORD in KB). Windows reads it and computes
  // nothing itself, so an entry without it shows an empty column. Measured from the installed files,
  // rounded up so a real installation never claims 0 KB. A value over 4 GB cannot be expressed in
  // this DWORD - the payload is ~400 MB, and no installation this installer writes comes close.
  RegWriteDWordValue(HKCU, Key, 'EstimatedSize', (DirSizeBytes(Root) + 1023) div 1024);
  RegWriteDWordValue(HKCU, Key, 'NoModify', 1);
  RegWriteDWordValue(HKCU, Key, 'NoRepair', 1);
  Log('DSH-AB: wrote the Add/Remove entry HKCU\' + Key + ' for ' + Root);
end;

// RecordedLinkPath reads one of the two shortcut paths DSH-AB keeps in its own entry
// (DshAbStartMenuLink / DshAbDesktopLink, written by WriteUninstallEntry and updated by
// src\dsh-ab\naming.go whenever the name changes). '' means the value is not there.
function RecordedLinkPath(const Value: String): String;
begin
  Result := '';
  RegQueryStringValue(HKCU, UninstallRegPath, Value, Result);
end;

// RemoveRecordedShortcuts deletes the shortcuts where DSH-AB last put them. The installer's own
// uninstall log only knows the paths from install time, and the name changes with the ports around
// it (DSH-AB / DSH-AB (3190) / DSH-AB (<tag>)), so once the app has renamed or moved a shortcut the
// log points at a file that is not there and the real one survived the uninstall pointing at a
// deleted exe (2026-09-22 真机验证). 旧版留下的 <Programs>\{#AppName}\ 空文件夹跟着那条链接一起
// 消失——只删那个子文件夹，绝不碰 <Programs> 根本身，也绝不碰桌面目录（那是用户的）。
procedure RemoveRecordedShortcuts;
var
  Link, Folder: String;
begin
  Link := RecordedLinkPath('DshAbStartMenuLink');
  if Link <> '' then
  begin
    if DeleteFile(Link) then
      Log('DSH-AB: removed the Start menu shortcut ' + Link)
    else
      Log('DSH-AB: no Start menu shortcut at ' + Link);
    // 旧版把这一条放进 <Programs>\{#AppName}\，空文件夹顺手删掉；现在它就在根下，所以必须先排掉
    // 根本身（ExtractFileDir 给出的正是它），否则一个空的开始菜单 Programs 文件夹会被整个删掉。
    Folder := ExtractFileDir(Link);
    if (Folder <> '') and
       (CompareText(RemoveBackslashUnlessRoot(Folder),
                    RemoveBackslashUnlessRoot(ExpandConstant('{userprograms}'))) <> 0) and
       RemoveDir(Folder) then
      Log('DSH-AB: removed the empty Start menu folder ' + Folder);
  end;
  Link := RecordedLinkPath('DshAbDesktopLink');
  if Link <> '' then
  begin
    if DeleteFile(Link) then
      Log('DSH-AB: removed the desktop shortcut ' + Link)
    else
      Log('DSH-AB: no desktop shortcut at ' + Link);
  end;
end;

// RemoveUninstallEntry deletes exactly the entry WriteUninstallEntry wrote, recomputed from the same
// directory. It is a key of its own, so the copy being uninstalled cannot take another copy's entry
// with it. Runs at usPostUninstall, i.e. only once the uninstall really happens - a cancelled
// uninstall keeps its entry.
procedure RemoveUninstallEntry;
var
  Key: String;
begin
  Key := UninstallRegPath;
  if RegKeyExists(HKCU, Key) then
  begin
    RegDeleteKeyIncludingSubkeys(HKCU, Key);
    Log('DSH-AB: removed the Add/Remove entry HKCU\' + Key);
  end
  else
    Log('DSH-AB: no Add/Remove entry to remove at HKCU\' + Key);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    WritePorts;
    BakeInstallPaths;
    WriteUninstallEntry;
  end;
end;

// NoteIfLeft records Path as undeleted, but only while it really is still there: a delete call can
// report failure for a file Windows removed a moment later (or for a directory that is gone now), and
// the user must not be sent after something that is no longer on disk.
procedure NoteIfLeft(var Remaining: string; const Path: string);
begin
  if DirExists(Path) or FileExists(Path) then
    Remaining := AddLine(Remaining, Path);
end;

// RemoveSlotKeepData clears one slot but keeps its data directory (the DSH_HOME).
procedure RemoveSlotKeepData(const SlotDir, KeepName: string; var Remaining: string);
var
  FindRec: TFindRec;
  Full: string;
begin
  if not FindFirst(AddBackslash(SlotDir) + '*', FindRec) then
    Exit;
  try
    repeat
      if (FindRec.Name = '.') or (FindRec.Name = '..') then
        Continue;
      if SameText(FindRec.Name, KeepName) then
        Continue;

      Full := AddBackslash(SlotDir) + FindRec.Name;
      if (FindRec.Attributes and $10) <> 0 then
      begin
        if not DelTree(Full, True, True, True) then
          NoteIfLeft(Remaining, Full);
      end
      else if not DeleteFile(Full) then
        NoteIfLeft(Remaining, Full);
    until not FindNext(FindRec);
  finally
    FindClose(FindRec);
  end;
end;

// RemoveAll clears the whole installation directory. With KeepData the two slot data
// directories survive, which is what the uninstall prompt defaults to.
//
// It used to return nothing and skip whatever it could not delete - in silence, which made a
// half-done uninstall look like a successful one: the entry and the shortcuts were gone while
// node_modules (held open by a dsh that was still running) stayed behind. Remaining now collects
// every file and directory that is still on disk afterwards, which is what the uninstaller shows
// the user. The kept data directories are never reported: they are kept on purpose.
procedure RemoveAll(const Dir: string; const KeepData: Boolean; var Remaining: string);
var
  FindRec: TFindRec;
  Full: string;
  IsDir: Boolean;
begin
  if not FindFirst(AddBackslash(Dir) + '*', FindRec) then
    Exit;
  try
    repeat
      if (FindRec.Name = '.') or (FindRec.Name = '..') then
        Continue;

      Full := AddBackslash(Dir) + FindRec.Name;
      IsDir := (FindRec.Attributes and $10) <> 0;

      if IsDir and KeepData and
         (SameText(FindRec.Name, 'slot-a') or SameText(FindRec.Name, 'slot-b')) then
        RemoveSlotKeepData(Full, 'data', Remaining)
      else if IsDir then
      begin
        if not DelTree(Full, True, True, True) then
          NoteIfLeft(Remaining, Full);
      end
      else if not DeleteFile(Full) then
        NoteIfLeft(Remaining, Full);
    until not FindNext(FindRec);
  finally
    FindClose(FindRec);
  end;
  // With KeepData the directory itself still holds the two data directories on purpose, so only the
  // full-delete path can complain about it.
  if (not KeepData) and (not RemoveDir(Dir)) then
    NoteIfLeft(Remaining, Dir);
end;

// ---- uninstall ---------------------------------------------------------------------------------
// Three things the uninstaller has to say, and the old one said none of them.
//
// 1. A DSH_AB.exe still running, or the dsh it started, holds its own files open: Windows then
//    refuses to delete them and the uninstall ends half-done. That is worth saying *before* anything
//    is removed, while the user can still go and exit the tray.
// 2. The user-data question now names the place the data is kept, so "keep" stays a decision the
//    user can find again afterwards.
// 3. RemoveAll deletes what it can and skips the rest without a word, so the uninstaller used to
//    report success over a directory that still held an entire node_modules tree. It now lists what
//    survived, with the one instruction that helps: delete it by hand.
//
// ---- ending this installation's own processes before the uninstall -----------------------------
// 卸载时问一次「要关闭它并继续卸载吗？」，选「是」就结束这些进程再继续。判定必须按安装
// 根来——taskkill /IM 会把别的安装（以及别的安装拉起的 node）一起杀掉，而两份安装共存是支持的。
// 匹配条件因此写成「进程名是 DSH_AB.exe 或 node.exe，且**可执行文件路径**落在本安装根之下」：
// 安装根里的 unins000.exe 和这次扫描用的 powershell.exe 都不在这两个名字里，不会被自己误杀。
// 只看路径、不看命令行：命令行里**提到**这个路径的进程（别的安装拉起的 node、跑脚本的 powershell、
// AI 的会话进程）不是本安装的进程，按命令行匹配就会误杀——2026-09-22 复验实测到过第三个这样的进程。
//
// 只用系统自带的 powershell.exe（不新增任何外部二进制），一次调用干到底：脚本最后 exit 的是匹配到
// 的进程数，Inno 从 ResultCode 拿到它，静默与交互两条路径都不需要临时文件。
function KillScript(const Root: String; Kill: Boolean): String;
var
  R, Tail: String;
begin
  R := AddBackslash(Root);
  // 用 Inno 自己的 StringChange，而不是 Delphi RTL 的 StringReplace：Pascal Script 里没有后者，
  // 用了这一整段 [Code] 编译不过（上一轮一次都没编译过，就是这么漏过去的）。单引号用 #39 写，
  // 不用把引号写成四个连在一起——那正是这段代码以前最容易读错的地方。
  StringChange(R, #39, #39 + #39);
  if Kill then
    // PowerShell 的 if 必须带括号：`if $n -gt 0 {…}` 是 ParserError，不是「条件为假」。2026-09-22
    // 真机验证就是栽在这里——脚本一行都没跑成、退出码 1，被调用方当成「匹配到 1 个进程」，日志于是
    // 写「结束了…共 1 个」而一个进程都没结束。
    Tail := '; if ($n -gt 0) { $p | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue } }'
  else
    Tail := '';
  Result :=
    '$r=''' + R + '''; $n=0;' +
    ' try { $p=@(Get-CimInstance Win32_Process | Where-Object {' +
    ' ($_.Name -eq ''DSH_AB.exe'' -or $_.Name -eq ''node.exe'') -and' +
    ' ($_.ExecutablePath -and $_.ExecutablePath.StartsWith($r, [StringComparison]::OrdinalIgnoreCase)) });' +
    ' $n=$p.Count' + Tail + ' } catch { exit -1 }; exit $n';
end;

// OwnRunningProcesses runs that scan; with Kill it also ends what it found. It returns the number
// of processes it matched, or -1 when the scan could not run at all (no PowerShell, a policy block,
// or the script itself failed): -1 must never be read as "nothing is running". That is why the scan
// exits -1 from its own catch instead of turning a failure into the count 0.
function OwnRunningProcesses(const Root: String; Kill: Boolean): Integer;
var
  ResultCode: Integer;
begin
  if not Exec('powershell.exe',
              '-NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "' +
              KillScript(Root, Kill) + '"',
              '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    Log('DSH-AB: 无法运行 powershell.exe 检查本安装根下的进程');
    Result := -1;
    Exit;
  end;
  Result := ResultCode;
end;

// UserDataDeleteRequested 读卸载器自己的命令行开关 /DELETEUSERDATA=1（大小写不敏感）。
//
// 用户数据那个询问走的是 SuppressibleMsgBox(..., MB_DEFBUTTON2, IDNO)：静默卸载自己答的是默认
// 按钮＝「保留」，所以「删」这一路在自动化里根本走不到——卸载器里没有任何地方能读到「这次要删」，
// 于是它既测不了、也没法被脚本驱动。开关让这条路可测：带了就照做（不再问），不带就照旧问一次。
// GetCmdTail 的官方说明就是「返回传给 Setup 或 Uninstall 的全部命令行参数」（Uninstall 也在内），
// 所以卸载器直接用得上，不必绕道 {param:}。只认整词，避免把 /DELETEUSERDATA=10 之类当成它。
function UserDataDeleteRequested(): Boolean;
var
  Tail: String;
begin
  Tail := Uppercase(GetCmdTail);
  Result := (Pos(' /DELETEUSERDATA=1 ', ' ' + Tail + ' ') > 0) or
            (Pos(' -DELETEUSERDATA=1 ', ' ' + Tail + ' ') > 0);
end;

// All three go through SuppressibleMsgBox: a silent uninstall answers them
// itself instead of blocking forever, and the log line is what a silent caller reads.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Remaining: string;
  Running, Killed: Integer;
begin
  if CurUninstallStep = usUninstall then
  begin
    // 正在运行的本安装进程会被问一次，而不是只被提醒。默认按钮是「是」，所以静默
    // 卸载（/SUPPRESSMSGBOXES）自己答「是」，也就真的能把文件删干净；选「否」照旧往下走，
    // 残留清单会告诉用户还剩什么。
    Running := OwnRunningProcesses(ExpandConstant('{app}'), False);
    if Running > 0 then
    begin
      if SuppressibleMsgBox('dsh 正在运行。要关闭它并继续卸载吗？' + #13#10 + #13#10 +
                '属于这份安装、还开着的 DSH_AB.exe / dsh 进程有 ' + IntToStr(Running) + ' 个。' + #13#10 +
                '选“是”会结束它们再继续卸载（只结束这份安装根下的）；选“否”照旧继续。' + #13#10 +
                '文件正被占用时卸载会删不掉，最后会列出还剩什么。',
                mbConfirmation, MB_YESNO, IDYES) = IDYES then
      begin
        Killed := OwnRunningProcesses(ExpandConstant('{app}'), True);
        Log('DSH-AB: 卸载前结束了本安装根下的进程，共 ' + IntToStr(Killed) + ' 个');
        // Windows 放下文件句柄还要一点时间，紧接着的删除才不会落空。
        Sleep(800);
      end;
    end
    else if Running < 0 then
      Log('DSH-AB: 跳过了运行中进程的检查（无法运行 powershell.exe）');

    // Keep by default. SuppressibleMsgBox answers itself with the
    // default button — IDNO, keep the data — so a silent uninstall never blocks and never deletes.
    // 静默卸载答的永远是那个默认按钮，这也是「删」这一路以前测不到的原因：/DELETEUSERDATA=1 带了就
    // 照做、不再问，脚本因此能真的跑一遍删除（verify-silent.ps1 的场景 2）。
    DeleteUserData := UserDataDeleteRequested();
    if not DeleteUserData then
      DeleteUserData := SuppressibleMsgBox('要同时删除 DSH-AB 的用户数据吗？' + #13#10 + #13#10 +
                '用户数据保留在 ' + ExpandConstant('{app}') + '\slot-*\data。' + #13#10 +
                '里面是会话记录、设置、插件数据。选“否”就把它们留在那里——“否”是默认选项。',
                mbConfirmation, MB_YESNO or MB_DEFBUTTON2, IDNO) = IDYES;
  end
  else if CurUninstallStep = usPostUninstall then
  begin
    // 两条快捷方式可能已经被 DSH-AB 改名或搬过家，所以按条目里记的真实路径删；路径就存在这个条目里，
    // 删条目的那一步必须在它之后。
    RemoveRecordedShortcuts;
    // The entry goes next: it names this installation, and it must not survive a RemoveAll that
    // fails halfway.
    RemoveUninstallEntry;
    Remaining := '';
    RemoveAll(ExpandConstant('{app}'), not DeleteUserData, Remaining);
    if Remaining <> '' then
    begin
      // Log first: under /SUPPRESSMSGBOXES the dialog answers itself, and this line is then the only
      // record of what the uninstall could not remove.
      Log('DSH-AB: 卸载后仍然存在、需要手动删除的项：' + #13#10 + Remaining);
      SuppressibleMsgBox('以下没删掉，请手动删除：' + #13#10 + #13#10 + Remaining + #13#10 + #13#10 +
                '原因通常是文件正被占用：请先在托盘选「退出」、结束 DSH_AB.exe 和 dsh 进程，再删这些。',
                mbError, MB_OK, IDOK);
    end
    else
      Log('DSH-AB: 卸载完成，安装目录里没有留下任何东西');
  end;
end;
