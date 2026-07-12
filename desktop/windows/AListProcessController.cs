using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace AListDesktop.Windows;

internal sealed class PreferredPortUnavailableException : InvalidOperationException
{
    public PreferredPortUnavailableException() : base("Port 5244 is already in use.")
    {
    }
}

internal sealed class AListProcessController
{
    private const int PortStart = 5244;
    private const int PortEnd = 5264;

    private static readonly UTF8Encoding Utf8WithBom = new(encoderShouldEmitUTF8Identifier: true);
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        WriteIndented = true
    };

    private readonly AListPreferencesStore _preferences;
    private readonly HttpClient _httpClient = new()
    {
        Timeout = TimeSpan.FromSeconds(1)
    };
    private readonly object _sync = new();

    private Process? _process;
    private Task<Uri>? _activeStartTask;
    private StreamWriter? _hostLogWriter;
    private StreamWriter? _processLogWriter;

    public AListProcessController()
    {
        var appData = Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData);
        RuntimeRoot = Path.Combine(appData, "AListDesktop");
        DataDirectory = Path.Combine(RuntimeRoot, "data");
        LogsDirectory = Path.Combine(RuntimeRoot, "logs");
        RunDirectory = Path.Combine(RuntimeRoot, "run");
        ConfigPath = Path.Combine(DataDirectory, "config.json");
        PidFilePath = Path.Combine(RunDirectory, "alist.pid");
        HostLogPath = Path.Combine(LogsDirectory, "desktop.log");
        ProcessLogPath = Path.Combine(LogsDirectory, "alist.log");
        _preferences = new AListPreferencesStore(Path.Combine(DataDirectory, "desktop-settings.json"));
    }

    public string RuntimeRoot { get; }
    public string DataDirectory { get; }
    public string LogsDirectory { get; }
    public string RunDirectory { get; }
    public string ConfigPath { get; }
    public string PidFilePath { get; }
    public string HostLogPath { get; }
    public string ProcessLogPath { get; }
    public Uri? CurrentServiceUrl { get; private set; }

    public bool AllowsLanAccess
    {
        get => _preferences.AllowsLanAccess;
        set => _preferences.AllowsLanAccess = value;
    }

    private string BindingHost => AllowsLanAccess ? "0.0.0.0" : "127.0.0.1";

    public Task<Uri> StartAsync(bool allowFallbackPort = false, CancellationToken cancellationToken = default)
    {
        lock (_sync)
        {
            if (_activeStartTask is not null)
            {
                return _activeStartTask;
            }

            if (_process is { HasExited: false } && CurrentServiceUrl is not null)
            {
                return Task.FromResult(CurrentServiceUrl);
            }

            _activeStartTask = StartInternalAsync(allowFallbackPort, cancellationToken);
            return _activeStartTask;
        }
    }

    public async Task<Uri> RestartAsync(bool allowFallbackPort = false, CancellationToken cancellationToken = default)
    {
        await StopAsync();
        return await StartAsync(allowFallbackPort, cancellationToken);
    }

    public async Task StopAsync()
    {
        Task<Uri>? activeTask;
        lock (_sync)
        {
            activeTask = _activeStartTask;
            _activeStartTask = null;
        }

        if (activeTask is not null && !activeTask.IsCompleted)
        {
            try
            {
                await activeTask;
            }
            catch
            {
            }
        }

        var process = _process;
        if (process is null)
        {
            RemovePidFile();
            return;
        }

        AppendHostLog($"Stopping AList process {process.Id}");
        try
        {
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
                await process.WaitForExitAsync();
            }
        }
        catch
        {
        }

        CleanupAfterExit();
    }

    private async Task<Uri> StartInternalAsync(bool allowFallbackPort, CancellationToken cancellationToken)
    {
        try
        {
            cancellationToken.ThrowIfCancellationRequested();
            EnsureRuntimeDirectories();
            var binaryPath = ResolveEmbeddedAListPath();
            TerminateOrphanedManagedProcessIfNeeded(binaryPath);

            var port = ChoosePort(allowFallbackPort);
            var serviceUrl = new Uri($"http://127.0.0.1:{port}/");
            WriteRuntimeConfiguration(port, serviceUrl);

            AppendHostLog($"Launching AList on port {port} with host {BindingHost}");

            _processLogWriter = new StreamWriter(new FileStream(ProcessLogPath, FileMode.Append, FileAccess.Write, FileShare.ReadWrite), Utf8WithBom)
            {
                AutoFlush = true
            };

            var processStartInfo = new ProcessStartInfo
            {
                FileName = binaryPath,
                Arguments = $"server --data \"{DataDirectory}\" --log-std",
                WorkingDirectory = DataDirectory,
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                StandardOutputEncoding = Encoding.UTF8,
                StandardErrorEncoding = Encoding.UTF8
            };

            processStartInfo.Environment["HOME"] = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            processStartInfo.Environment["TMPDIR"] = Path.Combine(DataDirectory, "temp");
            processStartInfo.Environment["ALIST_DESKTOP_MODE"] = "1";

            var process = new Process
            {
                StartInfo = processStartInfo,
                EnableRaisingEvents = true
            };
            process.OutputDataReceived += (_, args) => WriteProcessLog(args.Data);
            process.ErrorDataReceived += (_, args) => WriteProcessLog(args.Data);
            process.Exited += (_, _) =>
            {
                AppendHostLog("AList process exited");
                CleanupAfterExit();
            };

            if (!process.Start())
            {
                throw new InvalidOperationException("AList failed to launch.");
            }

            process.BeginOutputReadLine();
            process.BeginErrorReadLine();

            _process = process;
            CurrentServiceUrl = serviceUrl;
            File.WriteAllText(PidFilePath, process.Id.ToString());

            try
            {
                await WaitUntilReadyAsync(serviceUrl, process, cancellationToken);
                AppendHostLog($"AList became ready at {serviceUrl}");
                return serviceUrl;
            }
            catch (Exception ex)
            {
                AppendHostLog($"AList failed to become ready: {ex.Message}");
                await StopStartedProcessAsync(process);
                throw;
            }
        }
        catch
        {
            CleanupAfterExit();
            throw;
        }
        finally
        {
            lock (_sync)
            {
                _activeStartTask = null;
            }
        }
    }

    private void EnsureRuntimeDirectories()
    {
        Directory.CreateDirectory(RuntimeRoot);
        Directory.CreateDirectory(DataDirectory);
        Directory.CreateDirectory(LogsDirectory);
        Directory.CreateDirectory(RunDirectory);
        Directory.CreateDirectory(Path.Combine(DataDirectory, "temp"));
    }

    private string ResolveEmbeddedAListPath()
    {
        var configured = Environment.GetEnvironmentVariable("ALIST_DESKTOP_ALIST_BINARY");
        if (!string.IsNullOrWhiteSpace(configured) && File.Exists(configured))
        {
            return configured;
        }

        var bundledPath = Path.Combine(AppContext.BaseDirectory, "Assets", "bin", "alist.exe");
        if (File.Exists(bundledPath))
        {
            return bundledPath;
        }

        throw new FileNotFoundException("The embedded AList binary was not found. Rebuild the Windows bundle before launching.", bundledPath);
    }

    private void WriteRuntimeConfiguration(int port, Uri serviceUrl)
    {
        JsonObject root;
        if (File.Exists(ConfigPath) && new FileInfo(ConfigPath).Length > 0)
        {
            try
            {
                root = JsonNode.Parse(File.ReadAllText(ConfigPath)) as JsonObject
                    ?? throw new InvalidOperationException("AList config must be a JSON object.");
            }
            catch (JsonException ex)
            {
                throw new InvalidOperationException($"AList configuration is invalid JSON: {ConfigPath}", ex);
            }
        }
        else
        {
            root = new JsonObject();
        }

        var scheme = root["scheme"] as JsonObject ?? new JsonObject();
        scheme["address"] = BindingHost;
        scheme["http_port"] = port;
        scheme["https_port"] = -1;
        root["scheme"] = scheme;

        if (AllowsLanAccess)
        {
            root.Remove("site_url");
        }
        else
        {
            root["site_url"] = serviceUrl.ToString().TrimEnd('/');
        }

        File.WriteAllText(ConfigPath, root.ToJsonString(JsonOptions));
        AppendHostLog($"Wrote runtime config to {ConfigPath}");
    }

    private int ChoosePort(bool allowFallbackPort)
    {
        if (IsPortAvailable(PortStart))
        {
            return PortStart;
        }

        if (!allowFallbackPort)
        {
            throw new PreferredPortUnavailableException();
        }

        for (var port = PortStart + 1; port <= PortEnd; port += 1)
        {
            if (IsPortAvailable(port))
            {
                return port;
            }
        }

        throw new InvalidOperationException("No available local port was found in the range 5245-5264.");
    }

    private bool IsPortAvailable(int port)
    {
        try
        {
            using var listener = new TcpListener(IPAddress.Parse(BindingHost), port);
            listener.Start();
            listener.Stop();
            return true;
        }
        catch
        {
            return false;
        }
    }

    private async Task WaitUntilReadyAsync(Uri serviceUrl, Process process, CancellationToken cancellationToken)
    {
        while (true)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (process.HasExited)
            {
                throw new InvalidOperationException("The AList process exited before becoming ready.");
            }

            try
            {
                using var response = await _httpClient.GetAsync(serviceUrl, cancellationToken);
                if ((int)response.StatusCode < 500)
                {
                    return;
                }
            }
            catch
            {
            }

            await Task.Delay(250, cancellationToken);
        }
    }

    private void TerminateOrphanedManagedProcessIfNeeded(string binaryPath)
    {
        if (File.Exists(PidFilePath))
        {
            var rawPid = File.ReadAllText(PidFilePath).Trim();
            if (int.TryParse(rawPid, out var pid))
            {
                TryKillProcess(pid, "orphaned managed AList process");
            }

            RemovePidFile();
        }

        TerminateOrphanedBundledAListProcesses(binaryPath);
    }

    private void TerminateOrphanedBundledAListProcesses(string binaryPath)
    {
        var currentProcessId = Environment.ProcessId;
        foreach (var process in Process.GetProcessesByName("alist"))
        {
            using (process)
            {
                try
                {
                    if (process.Id == currentProcessId || process.HasExited)
                    {
                        continue;
                    }

                    var executablePath = process.MainModule?.FileName;
                    if (!IsManagedBundledAListProcess(executablePath, binaryPath))
                    {
                        continue;
                    }

                    TryKillProcess(process.Id, "orphaned bundled AList process");
                }
                catch
                {
                }
            }
        }
    }

    private static bool IsManagedBundledAListProcess(string? executablePath, string binaryPath)
    {
        if (string.IsNullOrWhiteSpace(executablePath))
        {
            return false;
        }

        if (string.Equals(executablePath, binaryPath, StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        var normalizedPath = executablePath.Replace(Path.AltDirectorySeparatorChar, Path.DirectorySeparatorChar);
        var bundledSuffix = $"{Path.DirectorySeparatorChar}AList{Path.DirectorySeparatorChar}Assets{Path.DirectorySeparatorChar}bin{Path.DirectorySeparatorChar}alist.exe";
        return normalizedPath.EndsWith(bundledSuffix, StringComparison.OrdinalIgnoreCase);
    }

    private void TryKillProcess(int pid, string reason)
    {
        try
        {
            using var process = Process.GetProcessById(pid);
            if (!process.HasExited)
            {
                AppendHostLog($"Found {reason} {pid}, terminating it");
                process.Kill(entireProcessTree: true);
                process.WaitForExit(3000);
            }
        }
        catch
        {
        }
    }

    private async Task StopStartedProcessAsync(Process process)
    {
        try
        {
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
                await process.WaitForExitAsync();
            }
        }
        catch
        {
        }

        CleanupAfterExit();
    }

    private void CleanupAfterExit()
    {
        _process = null;
        CurrentServiceUrl = null;
        _processLogWriter?.Dispose();
        _processLogWriter = null;
        RemovePidFile();
    }

    private void WriteProcessLog(string? line)
    {
        if (!string.IsNullOrWhiteSpace(line))
        {
            _processLogWriter?.WriteLine(line);
        }
    }

    private void RemovePidFile()
    {
        try
        {
            if (File.Exists(PidFilePath))
            {
                File.Delete(PidFilePath);
            }
        }
        catch
        {
        }
    }

    private void AppendHostLog(string message)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(HostLogPath) ?? ".");
            _hostLogWriter ??= new StreamWriter(new FileStream(HostLogPath, FileMode.Append, FileAccess.Write, FileShare.ReadWrite), Utf8WithBom)
            {
                AutoFlush = true
            };
            _hostLogWriter.WriteLine($"[{DateTimeOffset.Now:O}] {message}");
        }
        catch
        {
        }
    }
}
