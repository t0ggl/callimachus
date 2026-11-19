import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { SettingsView } from '../views/SettingsView';
import type {
  LibrarySettings,
  ProwlarrSettings,
  QualityProfile,
  SettingsOverridesMap,
} from '../types';

const baseLibrary: LibrarySettings = {
  rootPath: '/env/books',
  stagingPollInterval: '5s',
  indexerSyncInterval: '15m0s',
};

const baseProwlarr: ProwlarrSettings = {
  url: 'http://prowlarr:9696',
  apiKeyConfigured: true,
  syncInterval: '1h0m0s',
};

const baseQuality: QualityProfile = {
  id: 'default',
  name: 'Standard Audiobook',
  allowedLanguages: ['en'],
  allowedFormats: ['EPUB', 'M4B'],
  preferSingleFileM4b: true,
  maxAudiobookSizeMb: 4096,
  maxEbookSizeBytes: 100 * 1024 * 1024,
};

const baseOverrides: SettingsOverridesMap = {
  prowlarr_url: {
    isOverridden: true,
    overriddenBy: 'CALLIMACHUS_PROWLARR_URL',
    envVar: 'CALLIMACHUS_PROWLARR_URL',
  },
  library_root: {
    isOverridden: true,
    overriddenBy: 'CALLIMACHUS_LIBRARY_ROOT',
    envVar: 'CALLIMACHUS_LIBRARY_ROOT',
  },
  max_audiobook_size_mb: {
    isOverridden: true,
    overriddenBy: 'CALLIMACHUS_MAX_AUDIOBOOK_SIZE_MB',
    envVar: 'CALLIMACHUS_MAX_AUDIOBOOK_SIZE_MB',
  },
  max_ebook_size_mb: {
    isOverridden: true,
    overriddenBy: "CALLIMACHUS_MAX_EBOOK_SIZE_MB",
    envVar: "CALLIMACHUS_MAX_EBOOK_SIZE_MB",
  },
  allowed_formats: {
    isOverridden: true,
    overriddenBy: 'CALLIMACHUS_ALLOWED_FORMATS',
    envVar: 'CALLIMACHUS_ALLOWED_FORMATS',
  },
};

describe('SettingsView Environment Overrides', () => {
  it('disables overridden controls and displays environment variable lock notes across tabs without auth UI', async () => {
    const user = userEvent.setup({ delay: null });

    render(
      <SettingsView
        indexers={[]}

        librarySettings={baseLibrary}
        prowlarrSettings={baseProwlarr}
        qualityProfile={baseQuality}
        overrides={baseOverrides}
      />
    );

    // Authentication & OIDC tab and inputs must not be rendered (auth is env-only)
    expect(screen.queryByTestId('tab-settings-auth')).not.toBeInTheDocument();
    expect(screen.queryByText(/Authentication & OIDC/i)).not.toBeInTheDocument();

    // Summary badge reflects active overrides
    expect(screen.getByTestId('env-override-summary')).toHaveTextContent('5 env overrides active');

    // Tab 1: Prowlarr URL is disabled with exact note, Prowlarr Sync Interval remains editable
    const prowlarrUrlInput = screen.getByLabelText('Prowlarr URL') as HTMLInputElement;
    expect(prowlarrUrlInput).toBeDisabled();
    expect(prowlarrUrlInput.value).toBe('http://prowlarr:9696');
    expect(screen.getByTestId('override-note-prowlarr_url')).toHaveTextContent(
      'Managed via environment variable CALLIMACHUS_PROWLARR_URL and cannot be modified here.'
    );
    expect(screen.getByLabelText('Prowlarr Sync Interval')).toBeEnabled();

    // Tab 3: Library & Storage
    await user.click(screen.getByTestId('tab-settings-library'));
    const libraryRootInput = screen.getByLabelText('Library Root Directory') as HTMLInputElement;
    expect(libraryRootInput).toBeDisabled();
    expect(libraryRootInput.value).toBe('/env/books');
    expect(screen.getByTestId('override-note-library_root')).toHaveTextContent(
      'Managed via environment variable CALLIMACHUS_LIBRARY_ROOT and cannot be modified here.'
    );
    expect(screen.getByLabelText('Staging Poll Interval')).toBeEnabled();

    // Tab 4: Quality & Profiles
    await user.click(screen.getByTestId('tab-settings-quality'));
    expect(screen.getByLabelText('Max Audiobook Size (MB)')).toBeDisabled();
    expect(screen.getByLabelText('Maximum eBook File Size (MB)')).toBeDisabled();
    expect(screen.getByTestId('override-note-max_ebook_size_mb')).toHaveTextContent('CALLIMACHUS_MAX_EBOOK_SIZE_MB');
    expect(screen.getByTestId('override-note-max_audiobook_size_mb')).toHaveTextContent(
      'Managed via environment variable CALLIMACHUS_MAX_AUDIOBOOK_SIZE_MB and cannot be modified here.'
    );
    for (const format of ['EPUB', 'AZW3', 'MOBI', 'CBR', 'CBZ']) {
      expect(screen.getByRole('button', { name: format })).toBeDisabled();
    }
    expect(screen.queryByLabelText('Audio Min Bitrate (kbps)')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Audio Target Bitrate (kbps)')).not.toBeInTheDocument();
  });

  it('saves only non-overridden settings and omits authSettings from payload', async () => {
    const user = userEvent.setup({ delay: null });
    const onSaveSettings = vi.fn().mockResolvedValue(undefined);

    render(
      <SettingsView
        indexers={[]}

        librarySettings={baseLibrary}
        prowlarrSettings={baseProwlarr}
        qualityProfile={baseQuality}
        overrides={baseOverrides}
        onSaveSettings={onSaveSettings}
      />
    );

    // Edit a non-overridden field on Library tab
    await user.click(screen.getByTestId('tab-settings-library'));
    const pollInput = screen.getByLabelText('Staging Poll Interval');
    await user.clear(pollInput);
    await user.type(pollInput, '15s');

    // Edit a non-overridden field on Quality tab
    await user.click(screen.getByTestId('tab-settings-quality'));
    expect(screen.queryByLabelText('Audio Min Bitrate (kbps)')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Audio Target Bitrate (kbps)')).not.toBeInTheDocument();

    // Save
    await user.click(screen.getByTestId('save-settings-btn'));

    expect(onSaveSettings).toHaveBeenCalledTimes(1);
    const payload = onSaveSettings.mock.calls[0][0];

    expect(payload.library).toEqual({
      completedDownloadRoot: "",
      stagingPollInterval: '15s',
      indexerSyncInterval: '15m0s',
    });
    expect(payload.library.rootPath).toBeUndefined();

    expect(payload.prowlarr).toEqual({
      syncInterval: '1h0m0s',
    });
    expect(payload.prowlarr.url).toBeUndefined();

    expect(payload.qualityProfile).not.toHaveProperty('audioMinBitrateKbps');
    expect(payload.qualityProfile).not.toHaveProperty('audioTargetBitrateKbps');
    expect(payload.qualityProfile.maxAudiobookSizeMb).toBeUndefined();
    expect(payload.qualityProfile.maxEbookSizeBytes).toBeUndefined();
    expect(payload.qualityProfile.allowedFormats).toBeUndefined();

    expect('authSettings' in payload).toBe(false);
    expect(await screen.findByTestId('settings-save-success')).toHaveTextContent(
      'Settings saved successfully.'
    );
  });
});

it('offers ebook formats while keeping audiobook handling automatic', async () => {
  const user = userEvent.setup({ delay: null });
  const onSaveSettings = vi.fn().mockResolvedValue(undefined);
  render(
    <SettingsView
      indexers={[]}

      librarySettings={baseLibrary}
      prowlarrSettings={baseProwlarr}
      qualityProfile={baseQuality}
      onSaveSettings={onSaveSettings}
    />
  );
  await user.click(screen.getByTestId('tab-settings-quality'));
  const formats = ['AZW3', 'MOBI', 'CBR', 'CBZ'];
  for (const format of formats) {
    const button = screen.getByRole('button', { name: format });
    expect(button).toBeEnabled();
    await user.click(button);
    expect(button).toHaveAttribute('aria-pressed', 'true');
  }
  expect(screen.queryByRole('button', { name: /^M4B/ })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /^MP3/ })).not.toBeInTheDocument();
  expect(screen.queryByRole('checkbox', { name: /convert audiobooks/i })).not.toBeInTheDocument();
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(onSaveSettings).toHaveBeenCalledWith(expect.objectContaining({
    qualityProfile: expect.objectContaining({
      allowedFormats: ['EPUB', 'M4B', ...formats],
    }),
  }));
});

it('edits and saves the ebook ceiling in bytes while displaying MB', async () => {
  const user = userEvent.setup({ delay: null });
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsView indexers={[]}  qualityProfile={baseQuality} onSaveSettings={save} />);
  await user.click(screen.getByTestId('tab-settings-quality'));
  const input = screen.getByLabelText('Maximum eBook File Size (MB)');
  expect(input).toHaveValue(100);
  await user.clear(input);
  await user.type(input, '150');
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(save).toHaveBeenCalledWith(expect.objectContaining({
    qualityProfile: expect.objectContaining({ maxEbookSizeBytes: 150 * 1024 * 1024 }),
  }));
});

it('reorders ebook priorities in both directions and saves the sequence independently of allowed formats', async () => {
  const user = userEvent.setup({ delay: null });
  const onSaveSettings = vi.fn().mockResolvedValue(undefined);
  render(<SettingsView indexers={[]}  librarySettings={baseLibrary}
    prowlarrSettings={baseProwlarr} qualityProfile={baseQuality} onSaveSettings={onSaveSettings} />);
  await user.click(screen.getByTestId('tab-settings-quality'));
  expect(screen.getByRole('button', { name: 'Move EPUB up' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Move CBZ down' })).toBeDisabled();
  await user.click(screen.getByRole('button', { name: 'Move PDF up' }));
  await user.click(screen.getByRole('button', { name: 'Move EPUB down' }));
  expect(screen.getByRole('list', { name: 'eBook format priority' }).textContent).toMatch(/AZW3.*EPUB.*PDF.*MOBI.*CBR.*CBZ/);
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(onSaveSettings).toHaveBeenCalledWith(expect.objectContaining({ qualityProfile: expect.objectContaining({
    ebookFormatPreferences: ['AZW3', 'EPUB', 'PDF', 'MOBI', 'CBR', 'CBZ'],
    allowedFormats: ['EPUB', 'M4B'],
  }) }));
});

it('locks priority arrows and omits preferences from saves when managed by the environment', async () => {
  const user = userEvent.setup({ delay: null });
  const onSaveSettings = vi.fn().mockResolvedValue(undefined);
  render(<SettingsView indexers={[]}  librarySettings={baseLibrary}
    prowlarrSettings={baseProwlarr} qualityProfile={{...baseQuality, ebookFormatPreferences: ['PDF', 'EPUB']}}
    overrides={{ebook_format_preferences: {isOverridden: true, overriddenBy: 'CALLIMACHUS_EBOOK_FORMAT_PREFERENCES', envVar: 'CALLIMACHUS_EBOOK_FORMAT_PREFERENCES'}}}
    onSaveSettings={onSaveSettings} />);
  await user.click(screen.getByTestId('tab-settings-quality'));
  expect(screen.getByRole('button', { name: 'Move EPUB up' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Move PDF down' })).toBeDisabled();
  expect(screen.getByTestId('override-note-ebook_format_preferences')).toHaveTextContent('CALLIMACHUS_EBOOK_FORMAT_PREFERENCES');
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(onSaveSettings.mock.calls[0][0].qualityProfile).not.toHaveProperty('ebookFormatPreferences');
});

it('can add an unranked ebook format to a saved partial preference list', async () => {
  const user = userEvent.setup({ delay: null });
  const onSaveSettings = vi.fn().mockResolvedValue(undefined);
  render(<SettingsView indexers={[]}  librarySettings={baseLibrary}
    prowlarrSettings={baseProwlarr} qualityProfile={{...baseQuality, ebookFormatPreferences: ['PDF']}}
    onSaveSettings={onSaveSettings} />);
  await user.click(screen.getByTestId('tab-settings-quality'));
  await user.click(screen.getByRole('button', {name: 'Add EPUB to priorities'}));
  await user.click(screen.getByRole('button', {name: 'Move EPUB up'}));
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(onSaveSettings.mock.calls[0][0].qualityProfile.ebookFormatPreferences).toEqual(['EPUB', 'PDF']);
});

it('delegates clients to Prowlarr and locks the completed download directory override', async () => {
  const user = userEvent.setup({ delay: null });
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsView indexers={[]} qualityProfile={baseQuality}
    librarySettings={{...baseLibrary, completedDownloadRoot: '/downloads/completed'}}
    overrides={{completed_download_root: {isOverridden: true, overriddenBy: 'CALLIMACHUS_COMPLETED_DOWNLOAD_ROOT', envVar: 'CALLIMACHUS_COMPLETED_DOWNLOAD_ROOT'}}}
    onSaveSettings={save} />);
  expect(screen.queryByText('Download Clients')).not.toBeInTheDocument();
  await user.click(screen.getByTestId('tab-settings-library'));
  expect(screen.getByLabelText('Completed Download Directory')).toBeDisabled();
  expect(screen.getByLabelText('Completed Download Directory')).toHaveValue('/downloads/completed');
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(save.mock.calls[0][0].library).not.toHaveProperty('completedDownloadRoot');
});

it('saves eBook conversion options and respects environment locks', async () => {
  const user = userEvent.setup({ delay: null });
  const onSaveSettings = vi.fn().mockResolvedValue(undefined);
  const props = {indexers: [], librarySettings: baseLibrary, prowlarrSettings: baseProwlarr, qualityProfile: baseQuality, onSaveSettings};
  const {rerender} = render(<SettingsView {...props} />);
  await user.click(screen.getByTestId('tab-settings-quality'));
  const toggle = screen.getByRole('checkbox', {name: 'Automatically convert eBooks to target format'});
  expect(screen.getByLabelText('Target eBook format')).toBeDisabled();
  await user.click(toggle);
  await user.selectOptions(screen.getByLabelText('Target eBook format'), 'AZW3');
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(onSaveSettings).toHaveBeenLastCalledWith(expect.objectContaining({qualityProfile: expect.objectContaining({convertEbooks: true, ebookTargetFormat: 'AZW3'})}));
  rerender(<SettingsView {...props} overrides={Object.fromEntries(['convert_ebooks', 'ebook_target_format'].map(key => [key, {isOverridden: true, overriddenBy: `CALLIMACHUS_${key.toUpperCase()}`, envVar: `CALLIMACHUS_${key.toUpperCase()}`}]))} />);
  expect(toggle).toBeDisabled();
  expect(screen.getByLabelText('Target eBook format')).toBeDisabled();
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(onSaveSettings.mock.calls.at(-1)?.[0].qualityProfile).not.toHaveProperty('convertEbooks');
  expect(onSaveSettings.mock.calls.at(-1)?.[0].qualityProfile).not.toHaveProperty('ebookTargetFormat');
});

it('never exposes local admin password indicators', () => {
  render(<SettingsView indexers={[]} librarySettings={baseLibrary} prowlarrSettings={baseProwlarr} qualityProfile={baseQuality} />);
  expect(screen.queryByText(/Argon2id|Configured & Encrypted|admin password|local password/i)).not.toBeInTheDocument();
});

describe('Connection tests', () => {
  it.each([
    ['prowlarr', 'Prowlarr', 'Prowlarr API Key'],
    ['google-books', 'Google Books', 'Google Books API key (optional)'],
    ['hardcover', 'Hardcover', 'Hardcover API key'],
  ])('tests %s with unsaved values and shows progress and success without saving or syncing', async (service, label, field) => {
    const user = userEvent.setup({ delay: null });
    let finish!: (value: unknown) => void;
    const fetchMock = vi.fn(() => new Promise(resolve => { finish = resolve; }));
    vi.stubGlobal('fetch', fetchMock);
    const save = vi.fn(); const sync = vi.fn();
    try {
      render(<SettingsView indexers={[]} qualityProfile={baseQuality} prowlarrSettings={baseProwlarr} onSaveSettings={save} onSyncIndexers={sync} />);
      await user.type(screen.getByLabelText(field), 'draft-key');
      const button = screen.getByRole('button', { name: `Test ${label} connection` });
      await user.click(button);
      expect(button).toBeDisabled();
      expect(button).toHaveTextContent('Testing…');
      expect(fetchMock).toHaveBeenCalledWith(`/api/settings/${service}/test`, expect.objectContaining({
        method: 'POST', body: JSON.stringify(service === 'prowlarr' ? { url: baseProwlarr.url, apiKey: 'draft-key' } : { apiKey: 'draft-key' }),
      }));
      finish({ ok: true, json: async () => ({ message: 'Connection successful.' }) });
      expect(await screen.findByText('Connection successful.')).toBeInTheDocument();
      expect(button).toBeEnabled(); expect(save).not.toHaveBeenCalled(); expect(sync).not.toHaveBeenCalled();
    } finally { vi.unstubAllGlobals(); }
  });

  it.each(['prowlarr', 'google-books', 'hardcover'])('shows actionable %s failure and allows retry with blank saved credentials', async service => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.fn().mockResolvedValue({ ok: false, json: async () => ({ message: 'Authentication failed. Check the API key and service permissions.' }) });
    vi.stubGlobal('fetch', fetchMock);
    try {
      render(<SettingsView indexers={[]} qualityProfile={baseQuality} prowlarrSettings={baseProwlarr} />);
      const label = service === 'prowlarr' ? 'Prowlarr' : service === 'google-books' ? 'Google Books' : 'Hardcover';
      const button = screen.getByRole('button', { name: `Test ${label} connection` });
      await user.click(button);
      expect(await screen.findByText(/Authentication failed/)).toBeInTheDocument();
      expect(JSON.parse(fetchMock.mock.calls[0][1].body).apiKey).toBe('');
      expect(button).toBeEnabled();
      fetchMock.mockRejectedValueOnce(new Error('network'));
      await user.click(button);
      expect(await screen.findByText('Unable to test connection. Check your network and retry.')).toBeInTheDocument();
    } finally { vi.unstubAllGlobals(); }
  });

  it('keeps metadata in separate sections and allows testing locked providers', async () => {
    const user = userEvent.setup({ delay: null });
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ message: 'Connection successful.' }) });
    vi.stubGlobal('fetch', fetchMock);
    try {
      const overrides = Object.fromEntries(['google_books_api_key', 'hardcover_api_key', 'prowlarr_api_key'].map(key => [key, { isOverridden: true, overriddenBy: `CALLIMACHUS_${key.toUpperCase()}`, envVar: `CALLIMACHUS_${key.toUpperCase()}` }]));
      render(<SettingsView indexers={[]} qualityProfile={baseQuality} overrides={overrides} />);
      for (const [label, field] of [['Google Books', 'Google Books API key (optional)'], ['Hardcover', 'Hardcover API key'], ['Prowlarr', 'Prowlarr API Key']]) {
        expect(screen.getByLabelText(field)).toBeDisabled();
        await user.click(screen.getByRole('button', { name: `Test ${label} connection` }));
      }
      expect(screen.getByRole('region', { name: 'Google Books' })).toContainElement(screen.getByLabelText('Google Books API key (optional)'));
      expect(fetchMock).toHaveBeenCalledTimes(3);
      expect(screen.getByTestId('override-note-hardcover_api_key')).toHaveTextContent('CALLIMACHUS_HARDCOVER_API_KEY');
    } finally { vi.unstubAllGlobals(); }
  });
});

it('saves independent audiobook and ebook sizes with clear units', async () => {
  const user = userEvent.setup({ delay: null });
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsView indexers={[]} qualityProfile={baseQuality} onSaveSettings={save} />);
  await user.click(screen.getByTestId('tab-settings-quality'));
  const audio = screen.getByLabelText('Max Audiobook Size (MB)');
  const ebook = screen.getByLabelText('Maximum eBook File Size (MB)');
  expect(audio).toHaveAccessibleDescription(/only to audiobook downloads.*1 MB = 1,048,576 bytes/);
  expect(ebook).toHaveAccessibleDescription(/only to eBooks.*1 MB = 1,048,576 bytes/);
  await user.clear(audio);
  await user.type(audio, '50');
  await user.clear(ebook);
  await user.type(ebook, '200');
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(save).toHaveBeenCalledWith(expect.objectContaining({
    qualityProfile: expect.objectContaining({ maxAudiobookSizeMb: 50, maxEbookSizeBytes: 200 * 1024 * 1024 }),
  }));
});

describe('Log Level', () => {
  it('displays the four levels and saves a changed selection', async () => {
    const user = userEvent.setup({ delay: null });
    const save = vi.fn().mockResolvedValue(undefined);
    const props = { indexers: [], qualityProfile: baseQuality, onSaveSettings: save };
    const { rerender } = render(<SettingsView {...props} />);

    await user.click(screen.getByRole('button', { name: 'Logging' }));
    const select = screen.getByRole('combobox', { name: 'Log Level' });
    expect(select).toHaveValue('info');
    expect(screen.getAllByRole('option').map((option) => option.textContent)).toEqual(['Debug', 'Info', 'Warn', 'Error']);
    await user.selectOptions(select, 'debug');
    await user.click(screen.getByTestId('save-settings-btn'));
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ logLevel: 'debug' }));

    rerender(<SettingsView {...props} logLevel="error" />);
    expect(select).toHaveValue('error');
  });

  it('shows the effective environment value, disables editing, and omits it from saves', async () => {
    const user = userEvent.setup({ delay: null });
    const save = vi.fn().mockResolvedValue(undefined);
    const envVar = 'CALLIMACHUS_LOG_LEVEL';
    render(
      <SettingsView indexers={[]} qualityProfile={baseQuality} logLevel="warn" onSaveSettings={save}
        overrides={{ log_level: { isOverridden: true, overriddenBy: envVar, envVar } }} />
    );

    await user.click(screen.getByRole('button', { name: 'Logging' }));
    const select = screen.getByRole('combobox', { name: 'Log Level' });
    expect(select).toHaveValue('warn');
    expect(select).toBeDisabled();
    expect(select).toHaveAccessibleDescription(expect.stringContaining(envVar));
    await user.click(screen.getByTestId('save-settings-btn'));
    expect(save).toHaveBeenCalledOnce();
    expect(save.mock.calls[0][0]).not.toHaveProperty('logLevel');
  });
});
