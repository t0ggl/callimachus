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
  maxFileSizeMb: 4096,
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
  max_file_size_mb: {
    isOverridden: true,
    overriddenBy: 'CALLIMACHUS_MAX_FILE_SIZE_MB',
    envVar: 'CALLIMACHUS_MAX_FILE_SIZE_MB',
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
        downloadClients={[]}
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
    expect(screen.getByLabelText('Max File Size (MB)')).toBeDisabled();
    expect(screen.getByLabelText('Maximum eBook File Size (MB)')).toBeDisabled();
    expect(screen.getByTestId('override-note-max_ebook_size_mb')).toHaveTextContent('CALLIMACHUS_MAX_EBOOK_SIZE_MB');
    expect(screen.getByTestId('override-note-max_file_size_mb')).toHaveTextContent(
      'Managed via environment variable CALLIMACHUS_MAX_FILE_SIZE_MB and cannot be modified here.'
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
        downloadClients={[]}
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
    expect(payload.qualityProfile.maxFileSizeMb).toBeUndefined();
    expect(payload.qualityProfile.maxEbookSizeBytes).toBeUndefined();
    expect(payload.qualityProfile.allowedFormats).toBeUndefined();

    expect('authSettings' in payload).toBe(false);
    expect(await screen.findByTestId('settings-save-success')).toHaveTextContent(
      'Settings saved successfully.'
    );
  });
});

it('offers common ebook formats and saves selections with the exact audiobook conversion label', async () => {
  const user = userEvent.setup({ delay: null });
  const onSaveSettings = vi.fn().mockResolvedValue(undefined);
  render(
    <SettingsView
      indexers={[]}
      downloadClients={[]}
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
  const conversion = screen.getByRole('checkbox', {
    name: 'Automatically convert audiobooks to M4B format',
  });
  expect(conversion).toBeChecked();
  await user.click(conversion);
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(onSaveSettings).toHaveBeenCalledWith(expect.objectContaining({
    qualityProfile: expect.objectContaining({
      allowedFormats: ['EPUB', 'M4B', ...formats],
      preferSingleFileM4b: false,
    }),
  }));
});

it('edits and saves the ebook ceiling in bytes while displaying MB', async () => {
  const user = userEvent.setup({ delay: null });
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsView indexers={[]} downloadClients={[]} qualityProfile={baseQuality} onSaveSettings={save} />);
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
  render(<SettingsView indexers={[]} downloadClients={[]} librarySettings={baseLibrary}
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
  render(<SettingsView indexers={[]} downloadClients={[]} librarySettings={baseLibrary}
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
  render(<SettingsView indexers={[]} downloadClients={[]} librarySettings={baseLibrary}
    prowlarrSettings={baseProwlarr} qualityProfile={{...baseQuality, ebookFormatPreferences: ['PDF']}}
    onSaveSettings={onSaveSettings} />);
  await user.click(screen.getByTestId('tab-settings-quality'));
  await user.click(screen.getByRole('button', {name: 'Add EPUB to priorities'}));
  await user.click(screen.getByRole('button', {name: 'Move EPUB up'}));
  await user.click(screen.getByTestId('save-settings-btn'));
  expect(onSaveSettings.mock.calls[0][0].qualityProfile.ebookFormatPreferences).toEqual(['EPUB', 'PDF']);
});
