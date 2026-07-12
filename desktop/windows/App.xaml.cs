using System.Diagnostics;
using System.IO;
using System.Windows;
using Microsoft.Win32;
using Forms = System.Windows.Forms;

namespace AListDesktop.Windows;

public partial class App : System.Windows.Application
{
    private const string SingleInstanceMutexName = @"Global\AListDesktop.Windows";
    private const string LaunchAtLoginValueName = "AListDesktop";

    private Mutex? _instanceMutex;
    private Forms.NotifyIcon? _notifyIcon;
    private System.Drawing.Icon? _appIcon;
    private Forms.ToolStripMenuItem? _launchAtLoginItem;
    private Forms.ToolStripMenuItem? _allowLanAccessItem;
    private AListProcessController? _processController;
    private bool _isQuitting;

    protected override async void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);

        _instanceMutex = new Mutex(initiallyOwned: true, name: SingleInstanceMutexName, createdNew: out var createdNew);
        if (!createdNew)
        {
            Shutdown();
            return;
        }

        _processController = new AListProcessController();
        ConfigureNotifyIcon();

        try
        {
            await _processController.StartAsync();
        }
        catch (Exception error)
        {
            System.Windows.MessageBox.Show(
                $"AList server failed to start.\n\n{error.Message}",
                "AList",
                MessageBoxButton.OK,
                MessageBoxImage.Error);
            Shutdown();
        }
    }

    protected override async void OnExit(ExitEventArgs e)
    {
        _isQuitting = true;

        if (_notifyIcon is not null)
        {
            _notifyIcon.Visible = false;
            _notifyIcon.Dispose();
            _notifyIcon = null;
        }

        _appIcon?.Dispose();
        _appIcon = null;

        if (_processController is not null)
        {
            await _processController.StopAsync();
        }

        _instanceMutex?.ReleaseMutex();
        _instanceMutex?.Dispose();
        _instanceMutex = null;

        base.OnExit(e);
    }

    private void ConfigureNotifyIcon()
    {
        var contextMenu = new Forms.ContextMenuStrip();
        contextMenu.Items.Add("Open in Browser", null, async (_, _) => await OpenInBrowserAsync());
        contextMenu.Items.Add("Restart Service", null, async (_, _) => await RestartServiceAsync());
        contextMenu.Items.Add(new Forms.ToolStripSeparator());
        contextMenu.Items.Add("Show Data Directory", null, (_, _) => ShowDataDirectory());
        contextMenu.Items.Add("Show Logs Directory", null, (_, _) => ShowLogsDirectory());
        contextMenu.Items.Add(new Forms.ToolStripSeparator());

        _launchAtLoginItem = new Forms.ToolStripMenuItem("Launch at Login", null, (_, _) => ToggleLaunchAtLogin());
        _launchAtLoginItem.Checked = IsLaunchAtLoginEnabled();
        contextMenu.Items.Add(_launchAtLoginItem);

        _allowLanAccessItem = new Forms.ToolStripMenuItem("Allow LAN Access", null, async (_, _) => await ToggleAllowLanAccessAsync())
        {
            Checked = _processController?.AllowsLanAccess ?? false
        };
        contextMenu.Items.Add(_allowLanAccessItem);

        contextMenu.Items.Add(new Forms.ToolStripSeparator());
        contextMenu.Items.Add("Quit", null, async (_, _) => await QuitAsync());

        _notifyIcon = new Forms.NotifyIcon
        {
            Text = "AList",
            Visible = true,
            ContextMenuStrip = contextMenu,
            Icon = LoadAppIcon()
        };
        _notifyIcon.MouseDoubleClick += async (_, args) =>
        {
            if (args.Button == Forms.MouseButtons.Left)
            {
                await OpenInBrowserAsync();
            }
        };
    }

    private async Task OpenInBrowserAsync()
    {
        if (_processController is null)
        {
            return;
        }

        Uri serviceUrl;
        try
        {
            serviceUrl = _processController.CurrentServiceUrl ?? await _processController.StartAsync();
        }
        catch (Exception error)
        {
            System.Windows.MessageBox.Show(
                $"AList server is not ready yet.\n\n{error.Message}",
                "AList",
                MessageBoxButton.OK,
                MessageBoxImage.Error);
            return;
        }

        try
        {
            OpenUrlInDefaultBrowser(serviceUrl);
        }
        catch (Exception error)
        {
            System.Windows.MessageBox.Show(
                $"AList is running at {serviceUrl}, but Windows could not open the default browser.\n\n{error.Message}",
                "AList",
                MessageBoxButton.OK,
                MessageBoxImage.Error);
        }
    }

    private async Task RestartServiceAsync()
    {
        if (_processController is null)
        {
            return;
        }

        try
        {
            await _processController.RestartAsync();
            if (_allowLanAccessItem is not null)
            {
                _allowLanAccessItem.Checked = _processController.AllowsLanAccess;
            }
        }
        catch (Exception error)
        {
            System.Windows.MessageBox.Show(
                $"Unable to restart AList.\n\n{error.Message}",
                "AList",
                MessageBoxButton.OK,
                MessageBoxImage.Error);
        }
    }

    private void ShowDataDirectory()
    {
        if (_processController is null)
        {
            return;
        }

        OpenFolder(_processController.DataDirectory);
    }

    private void ShowLogsDirectory()
    {
        if (_processController is null)
        {
            return;
        }

        OpenFolder(_processController.LogsDirectory);
    }

    private async Task ToggleAllowLanAccessAsync()
    {
        if (_processController is null)
        {
            return;
        }

        try
        {
            _processController.AllowsLanAccess = !_processController.AllowsLanAccess;
            await _processController.RestartAsync();
            if (_allowLanAccessItem is not null)
            {
                _allowLanAccessItem.Checked = _processController.AllowsLanAccess;
            }
        }
        catch (Exception error)
        {
            System.Windows.MessageBox.Show(
                $"Unable to update LAN access.\n\n{error.Message}",
                "AList",
                MessageBoxButton.OK,
                MessageBoxImage.Error);
        }
    }

    private void ToggleLaunchAtLogin()
    {
        using var runKey = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Run", writable: true);
        if (runKey is null)
        {
            return;
        }

        if (IsLaunchAtLoginEnabled())
        {
            runKey.DeleteValue(LaunchAtLoginValueName, throwOnMissingValue: false);
        }
        else if (Environment.ProcessPath is { Length: > 0 } executablePath)
        {
            runKey.SetValue(LaunchAtLoginValueName, $"\"{executablePath}\"");
        }

        if (_launchAtLoginItem is not null)
        {
            _launchAtLoginItem.Checked = IsLaunchAtLoginEnabled();
        }
    }

    private bool IsLaunchAtLoginEnabled()
    {
        using var runKey = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Run", writable: false);
        var rawValue = runKey?.GetValue(LaunchAtLoginValueName)?.ToString() ?? string.Empty;
        var executablePath = Environment.ProcessPath ?? string.Empty;
        if (string.IsNullOrWhiteSpace(rawValue) || string.IsNullOrWhiteSpace(executablePath))
        {
            return false;
        }

        return rawValue.Contains(executablePath, StringComparison.OrdinalIgnoreCase);
    }

    private void OpenFolder(string path)
    {
        Directory.CreateDirectory(path);
        Process.Start(new ProcessStartInfo
        {
            FileName = "explorer.exe",
            Arguments = $"\"{path}\"",
            UseShellExecute = true
        });
    }

    private static void OpenUrlInDefaultBrowser(Uri url)
    {
        try
        {
            Process.Start(new ProcessStartInfo
            {
                FileName = url.ToString(),
                UseShellExecute = true
            });
            return;
        }
        catch
        {
        }

        Process.Start(new ProcessStartInfo
        {
            FileName = "explorer.exe",
            Arguments = url.ToString(),
            UseShellExecute = true
        });
    }

    private System.Drawing.Icon LoadAppIcon()
    {
        if (_appIcon is not null)
        {
            return _appIcon;
        }

        var iconPath = Path.Combine(AppContext.BaseDirectory, "Resources", "AListDesktop.ico");
        if (File.Exists(iconPath))
        {
            using var icon = new System.Drawing.Icon(iconPath);
            _appIcon = (System.Drawing.Icon)icon.Clone();
            return _appIcon;
        }

        _appIcon = System.Drawing.SystemIcons.Application;
        return _appIcon;
    }

    private async Task QuitAsync()
    {
        _isQuitting = true;
        await StopServiceAsync();
        Shutdown();
    }

    private async Task StopServiceAsync()
    {
        if (_processController is null)
        {
            return;
        }

        try
        {
            await _processController.StopAsync();
        }
        catch when (_isQuitting)
        {
        }
    }
}
