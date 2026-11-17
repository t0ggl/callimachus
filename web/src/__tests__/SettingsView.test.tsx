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
    expect(screen.getByTestId('env-override-summary')).toHaveTextContent('4 env overrides active');

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
    expect(screen.getByTestId('override-note-max_file_size_mb')).toHaveTextContent(
      'Managed via environment variable CALLIMACHUS_MAX_FILE_SIZE_MB and cannot be modified here.'
    );
    expect(screen.getByRole('button', { name: 'EPUB' })).toBeDisabled();
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
    expect(payload.qualityProfile.allowedFormats).toBeUndefined();

    expect('authSettings' in payload).toBe(false);
    expect(await screen.findByTestId('settings-save-success')).toHaveTextContent(
      'Settings saved successfully.'
    );
  });
});
