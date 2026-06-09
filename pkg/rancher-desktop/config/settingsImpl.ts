// This file contains the code to work with the settings.json file along with
// code docs on it.

import _ from 'lodash';

import {
  defaultSettings,
  Settings,
} from '@pkg/config/settings';

export function getSettings(): Settings {
  return defaultSettings;
}

export function firstRunDialogNeeded() {
  return false;
}

export function turnFirstRunOff() {
}

export function runInDebugMode(debug: boolean): boolean {
  return debug || !!process.env.RD_DEBUG_ENABLED;
}

// Imported from dashboard/config/settings.js
// Setting IDs
export const SETTING = { PL_RANCHER_VALUE: 'rancher' };
