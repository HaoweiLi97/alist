using System.IO;
using System.Text.Json;

namespace AListDesktop.Windows;

internal sealed class AListPreferencesStore
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        WriteIndented = true
    };

    private readonly string _settingsPath;
    private readonly object _sync = new();
    private PreferencesModel? _cached;

    public AListPreferencesStore(string settingsPath)
    {
        _settingsPath = settingsPath;
    }

    public bool AllowsLanAccess
    {
        get => Load().AllowsLanAccess;
        set
        {
            var settings = Load();
            settings.AllowsLanAccess = value;
            Save(settings);
        }
    }

    private PreferencesModel Load()
    {
        lock (_sync)
        {
            if (_cached is not null)
            {
                return _cached;
            }

            if (!File.Exists(_settingsPath))
            {
                _cached = new PreferencesModel();
                return _cached;
            }

            try
            {
                var json = File.ReadAllText(_settingsPath);
                _cached = JsonSerializer.Deserialize<PreferencesModel>(json) ?? new PreferencesModel();
            }
            catch
            {
                _cached = new PreferencesModel();
            }

            return _cached;
        }
    }

    private void Save(PreferencesModel settings)
    {
        lock (_sync)
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_settingsPath) ?? ".");
            File.WriteAllText(_settingsPath, JsonSerializer.Serialize(settings, JsonOptions));
            _cached = settings;
        }
    }

    private sealed class PreferencesModel
    {
        public bool AllowsLanAccess { get; set; }
    }
}
