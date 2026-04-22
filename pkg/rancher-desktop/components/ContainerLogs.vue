<template>
  <div class="container-logs-component">
    <banner
      v-if="errorMessage"
      class="content-state"
      color="error"
      data-testid="error-message"
    >
      <span class="icon icon-info-circle icon-lg" />
      {{ errorMessage }}
    </banner>

    <loading-indicator
      v-if="!container || waitingForInitialLogs"
      class="content-state"
      data-testid="loading-indicator"
    >
      Loading logs...
    </loading-indicator>

    <div
      v-else
      ref="terminalContainer"
      class="terminal-container"
      data-testid="terminal"
    />
  </div>
</template>

<script lang="ts" setup>
import { Banner } from '@rancher/components';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Terminal } from '@xterm/xterm';
import { shell } from 'electron';
import { ref, onBeforeUnmount, watch, nextTick, useTemplateRef, computed } from 'vue';
import { useStore } from 'vuex';

import LoadingIndicator from '@pkg/components/LoadingIndicator.vue';
import { usePassthroughURL } from '@pkg/composables/passthrough';

defineOptions({ name: 'ContainerLogs' });

const { containerId, isRunning } = defineProps<{
  containerId: string;
  isRunning:   boolean;
}>();

defineExpose({
  clearSearch,
  performSearch,
  searchNext,
  searchPrevious,
});

const store = useStore();
const errorMessage = ref<string | undefined>();

let terminal: Terminal | undefined;
let fitAddon: FitAddon | undefined;
let searchAddon: SearchAddon | undefined;
let searchDebounceTimer: ReturnType<typeof setTimeout> | undefined;

let terminalAborter: AbortController | undefined;
let buffer: ArrayBuffer[] = [];
/**
 * waitingForInitialLogs is used to delay showing the terminal until after the
 * initial logs have streamed in.  This prevents the user seeing a quick stream
 * of logs coming in initially.
 */
const waitingForInitialLogs = ref(true);
const container = computed(() => {
  return store.getters['container-engine/containerById'](containerId) ?? null;
});

/**
 * Timer used with `waitingForInitialLogs` to reveal the terminal after a delay.
 */
let revealTimeout: ReturnType<typeof setTimeout> | undefined;

const genericLogURL = usePassthroughURL('logs');
const logURL = computed(() => {
  if (!genericLogURL.value || !container.value?.metadata?.name) {
    return undefined;
  }
  return new URL(container.value.metadata.name, genericLogURL.value).href;
});

// Ref used for triggering reloads when the connection dies.
const reconnectTrigger = ref(0);
let reconnectAttempts = 0;
let reconnectResetTimer: ReturnType<typeof setTimeout> | undefined;
const maxReconnectAttempts = 5;

const terminalContainer = useTemplateRef('terminalContainer');
// Hook up the terminal once the container is mounted.
watch(terminalContainer, async(terminalContainer, _oldValue, onCleanup) => {
  terminalAborter?.abort();
  terminalAborter = undefined;

  if (!terminalContainer) {
    return;
  }

  terminalAborter = new AbortController();
  onCleanup(() => terminalAborter?.abort());

  const t = new Terminal({
    theme: {
      background:          '#1a1a1a',
      foreground:          '#e0e0e0',
      cursor:              '#1a1a1a', // same as the background to effectively hide the cursor.
      black:               '#000000',
      red:                 '#ff5555',
      green:               '#50fa7b',
      yellow:              '#f1fa8c',
      blue:                '#8be9fd',
      magenta:             '#ff79c6',
      cyan:                '#8be9fd',
      white:               '#f8f8f2',
      brightBlack:         '#6272a4',
      brightRed:           '#ff6e6e',
      brightGreen:         '#69ff94',
      brightYellow:        '#ffffa5',
      brightBlue:          '#d6acff',
      brightMagenta:       '#ff92df',
      brightCyan:          '#a4ffff',
      brightWhite:         '#ffffff',
    },
    fontSize:     14,
    fontFamily:   '"Courier New", "Monaco", monospace',
    cursorBlink:  false,
    disableStdin: true,
    convertEol:   true,
    scrollback:   50_000,
  });
  terminalAborter.signal.addEventListener('abort', () => {
    terminal = undefined;
    t.dispose();
  });

  fitAddon = new FitAddon();
  t.loadAddon(fitAddon);
  terminalAborter.signal.addEventListener('abort', () => {
    try {
      fitAddon?.dispose();
    } catch {
      // Ignore errors here: fitAddon.dispose() can throw incorrectly.
    }
    fitAddon = undefined;
  });

  searchAddon = new SearchAddon();
  t.loadAddon(searchAddon);
  terminalAborter.signal.addEventListener('abort', () => {
    searchAddon?.dispose();
    searchAddon = undefined;
  });

  t.loadAddon(new WebLinksAddon((event: MouseEvent, uri: string) => {
    event.preventDefault();
    shell.openExternal(uri);
  }));

  // Disable key events to allow normal behaviour such as copy/paste.
  t.attachCustomKeyEventHandler(() => false);
  (terminalContainer as any).__xtermTerminal = t;
  t.open(terminalContainer);

  await nextTick();
  if (buffer.length > 0) {
    // If we have data streamed in before the terminal showed up, write it now.
    // This shouldn't be necessary as we show the terminal as soon as the socket
    // opens, but it's still possible to end up with a race if the first message
    // comes in before the terminal container watch fires.
    for (const chunk of buffer) {
      t.write(new Uint8Array(chunk));
    }
    buffer = [];
  }
  terminal = t;

  const resizeObserver = new ResizeObserver(fitAddon.fit.bind(fitAddon));
  resizeObserver.observe(terminalContainer);
  terminalAborter.signal.addEventListener('abort', () => {
    resizeObserver.disconnect();
  });
  fitAddon.fit();
});

function isArrayBufferMessage(message: MessageEvent): message is MessageEvent<ArrayBuffer> {
  return message.data instanceof ArrayBuffer;
}

// Start streaming logs from the container.  We want to set `immediate` so that
// if `logURL` is already available by the time we set up the watcher, we don't
// have to wait for another change to trigger the log streaming.
watch([logURL, reconnectTrigger], async([logURL], [,], cleanUp) => {
  if (!logURL) {
    return;
  }
  errorMessage.value = undefined;
  const streamAborter = new AbortController();
  cleanUp(() => {
    streamAborter.abort();
    clearTimeout(revealTimeout);
    clearTimeout(reconnectTimer);
    clearTimeout(reconnectResetTimer);
  });
  streamAborter.signal.addEventListener('abort', () => {
    buffer = [];
    terminal?.reset();
  });

  // Wait a bit for the rendering to finish, so we don't end up creating many
  // connections before Vue finished its thing.  `nextTick` still causes
  // spurious events to fire, so use a longer timeout here.
  await new Promise(resolve => setTimeout(resolve, 200));
  if (streamAborter.signal.aborted) {
    return;
  }

  try {
    buffer = [];

    // Authentication for the log stream is handled in the main process via the
    // webRequest.onBeforeSendHeaders listener; see `@pkg/window/index.ts`.
    const socket = new WebSocket(logURL);
    socket.binaryType = 'arraybuffer';
    streamAborter.signal.addEventListener('abort', () => socket.close());
    socket.addEventListener('message', (event) => {
      if (!isArrayBufferMessage(event)) {
        return;
      }
      if (streamAborter.signal.aborted) {
        // We can get here if we closed the connection at the WebSocket level
        // (i.e. sent a "close" message), but the server hasn't paid attention
        // to it yet and kept sending things to us.
        socket.close();
        return;
      }
      const t = terminal;
      if (!t) {
        buffer.push(event.data);
        return;
      }
      errorMessage.value = undefined;
      t.write(new Uint8Array(event.data));
      if (waitingForInitialLogs.value) {
        // If we're still waiting for the initial logs, delay the reveal
        // until after all of the initial logs have streamed in.
        clearTimeout(revealTimeout);

        revealTimeout = setTimeout(() => {
          waitingForInitialLogs.value = false;
          nextTick(() => {
            fitAddon?.fit();
            terminal?.scrollToBottom();

            // Once we stop streaming in initial logs, reset the reconnect
            // attempts so that we can retry again later.
            reconnectAttempts = 0;
            clearTimeout(reconnectResetTimer);
          });
        }, 200);
      }
    });
    socket.addEventListener('open', () => {
      // Once the connection is open, reset the initial wait to avoid the logs
      // streaming in quickly.  Do not do the reset earlier, so that the user
      // can still look at the last known logs during a disconnect.
      waitingForInitialLogs.value = true;
      clearTimeout(revealTimeout);

      reconnectResetTimer = setTimeout(() => {
        // If we have no logs after a while, reset the reconnect attempts to
        // allow retries again.
        reconnectAttempts = 0;
      }, 10_000);

      revealTimeout = setTimeout(() => {
        waitingForInitialLogs.value = false;
        nextTick(() => {
          fitAddon?.fit();
          terminal?.scrollToBottom();
        });
      }, 500);
    });
    socket.addEventListener('error', (event) => {
      console.error('WebSocket error:', event);
      if (streamAborter.signal.aborted) {
        // Do not set error on intentional aborts.
        return;
      }
      let message = String('message' in event ? event.message : event);

      if (message.startsWith('[object')) {
        message = 'unknown error';
      }
      errorMessage.value = `WebSocket error: ${ message }`;
      handleStreamError();
    });
    socket.addEventListener('close', (event) => {
      console.log('WebSocket closed:', event);
      if (streamAborter.signal.aborted) {
        // Do not set error on intentional aborts.
        return;
      }
      if (!event.wasClean || event.code !== 1000) { // 1000 = normal closure
        errorMessage.value = `WebSocket closed unexpectedly: ${ event.reason || 'code ' + event.code }`;
        handleStreamError();
      }
    });
  } catch (err: any) {
    console.error('Error setting up log stream:', err);
    errorMessage.value = `Failed to load logs: ${ err.message || err }`;
  }
}, { immediate: true });

let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

function handleStreamError() {
  clearTimeout(reconnectTimer);
  if (!isRunning) {
    // If the container isn't running, we expect the log stream to fail, so
    // don't show a reconnect button in this case.
    return;
  }
  // On unexpected log streaming error, retry with exponential backoff by
  // touching the `reconnectTrigger` ref, which is a dependency of the log
  // streaming watcher.
  if (reconnectAttempts < maxReconnectAttempts) {
    const delay = Math.pow(2, reconnectAttempts) * 1_000;
    reconnectTimer = setTimeout(() => {
      reconnectAttempts++;
      // Trigger a reconnect.
      reconnectTrigger.value++;
    }, delay);
  }
}

function clearSearch() {
  searchAddon?.clearDecorations();
}

function performSearch(searchTerm: string) {
  clearTimeout(searchDebounceTimer);

  searchDebounceTimer = setTimeout(() => {
    if (!searchAddon) return;

    searchAddon.clearDecorations();
    if (searchTerm) {
      try {
        searchAddon.findNext(searchTerm);
      } catch (err) {
        console.error('Search error:', err);
      }
    }
  }, 300);
}

function searchNext(searchTerm: string) {
  if (!searchAddon || !searchTerm) return;
  executeSearch(() => searchAddon?.findNext(searchTerm));
}

function searchPrevious(searchTerm: string) {
  if (!searchAddon || !searchTerm) return;
  executeSearch(() => searchAddon?.findPrevious(searchTerm));
}

function executeSearch(searchFn: () => void) {
  try {
    searchFn();
  } catch (err) {
    console.error('Search error:', err);
  }
}

onBeforeUnmount(() => {
  terminalAborter?.abort();
  clearTimeout(searchDebounceTimer);
  clearTimeout(revealTimeout);
  clearTimeout(reconnectTimer);
  clearTimeout(reconnectResetTimer);
});
</script>

<style lang="scss" scoped>
@import '@xterm/xterm/css/xterm.css';

.container-logs-component {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
  flex: 1;
}

.content-state {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 2.5rem;
}

.terminal-container {
  background: #1a1a1a;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;

  :deep(.xterm) {
    height: 100%;
  }

  :deep(.xterm-selection) {
    overflow: hidden;
  }
}
</style>
