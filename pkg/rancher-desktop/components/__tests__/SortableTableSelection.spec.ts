// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { jest } from '@jest/globals';
import { mount } from '@vue/test-utils';
import { toRaw } from 'vue';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { sortableTableGlobal } from '@pkg/utils/testUtils/sortableTable';

const componentStub = { template: '<div />' };

mockModules({
  '@pkg/components/BadgeState.vue':          componentStub,
  '@pkg/components/Banner.vue':              componentStub,
  '@pkg/components/form/Checkbox.vue':       { template: '<div class="checkbox-container" />' },
  '@pkg/components/form/LabeledTooltip.vue': componentStub,
});

const { default: SortableTable } = await import('@pkg/components/SortableTable/index.vue');

interface Row {
  id:               string;
  containerName:    string;
  projectGroup:     string;
  metadata:         { uid: string };
  availableActions: { label: string, action: string, enabled: boolean, bulkable: boolean }[];
}

function row(id: string, projectGroup = 'one'): Row {
  return {
    id,
    containerName:    id,
    projectGroup,
    metadata:         { uid: `${ id }-uid` },
    availableActions: [{
      label: 'Stop', action: 'stopContainer', enabled: true, bulkable: true,
    }],
  };
}

function mountTable(rows: Row[], extraProps: Record<string, any> = {}) {
  return mount(SortableTable as any, {
    props: {
      headers:      [{ name: 'containerName', label: 'Name', sort: ['containerName'] }],
      keyField:     'id',
      rows,
      rowsPerPage:  10,
      rowActions:   false,
      tableActions: true,
      search:       false,
      ...extraProps,
    },
    slots: {
      'col:containerName': '<td><a class="container-name-link">{{ params.row.containerName }}</a></td>',
    },
    global: sortableTableGlobal(),
  });
}

const EXTENDS_SELECTION = { shiftKey: true };
const ADDS_TO_SELECTION = { ctrlKey: true, metaKey: true };

function click(wrapper: any, rowIndex: number, cellSelector: string, modifiers: Record<string, boolean> = {}) {
  const cell = wrapper.findAll('tr.main-row')[rowIndex].find(cellSelector).element;

  cell.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, ...modifiers }));
  cell.dispatchEvent(new MouseEvent('click', { bubbles: true, ...modifiers }));

  return wrapper.vm.$nextTick();
}

function clickCell(wrapper: any, rowIndex: number, modifiers: Record<string, boolean> = {}) {
  return click(wrapper, rowIndex, 'td:not(.row-check)', modifiers);
}

function clickContainerNameLink(wrapper: any, rowIndex: number, modifiers: Record<string, boolean> = {}) {
  return click(wrapper, rowIndex, '.container-name-link', modifiers);
}

function selectedRowsOf(wrapper: any) {
  return wrapper.vm.selectedRows;
}

function threeRows() {
  return [row('a'), row('b'), row('c')];
}

describe('SortableTable selection', () => {
  it('extends the selection to the shift-clicked row', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows);

    await clickCell(wrapper, 0);
    await clickCell(wrapper, 2, EXTENDS_SELECTION);

    expect(selectedRowsOf(wrapper)).toEqual(rows);
  });

  it('extends the selection when the shift-click lands on a link', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows);

    await clickContainerNameLink(wrapper, 0, EXTENDS_SELECTION);
    await clickContainerNameLink(wrapper, 2, EXTENDS_SELECTION);

    expect(selectedRowsOf(wrapper)).toEqual(rows);
  });

  it('extends the selection across groups', async() => {
    const rows = [row('a', 'one'), row('b', 'two'), row('c', 'two')];
    const wrapper = mountTable(rows, { groupBy: 'projectGroup' });

    await clickCell(wrapper, 0);
    await clickCell(wrapper, 2, EXTENDS_SELECTION);

    expect(selectedRowsOf(wrapper)).toEqual(rows);
  });

  it('deselects the range when shift-clicking a selected row', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows);

    await clickCell(wrapper, 2);
    await clickCell(wrapper, 0, EXTENDS_SELECTION);
    await clickCell(wrapper, 2, EXTENDS_SELECTION);

    expect(selectedRowsOf(wrapper)).toEqual([]);
  });

  it('deselects only the row that was clicked', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows);

    await clickCell(wrapper, 0);
    await clickCell(wrapper, 2, EXTENDS_SELECTION);
    await clickCell(wrapper, 1, ADDS_TO_SELECTION);

    expect(selectedRowsOf(wrapper)).toEqual([rows[0], rows[2]]);
  });

  it('keeps the selection when the rows are rebuilt', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows);

    await clickCell(wrapper, 0);
    await clickCell(wrapper, 1, EXTENDS_SELECTION);

    const rebuiltRows = threeRows();

    await wrapper.setProps({ rows: rebuiltRows });

    expect(selectedRowsOf(wrapper).map(toRaw)).toEqual([rebuiltRows[0], rebuiltRows[1]]);
  });

  it('keeps the selection of rebuilt rows when the key field is a path', async() => {
    const wrapper = mountTable(threeRows(), { keyField: 'metadata.uid' });

    await clickCell(wrapper, 0);

    const rebuiltRows = threeRows();

    await wrapper.setProps({ rows: rebuiltRows });

    expect(selectedRowsOf(wrapper).map(toRaw)).toEqual([rebuiltRows[0]]);
  });

  it('expands only the toggled row when the key field is a path', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows, { keyField: 'metadata.uid', subExpandColumn: true });

    (wrapper.vm as any).toggleExpand(rows[0]);
    await wrapper.vm.$nextTick();

    expect(wrapper.findAll('tr.main-row .icon-chevron-down')).toHaveLength(1);
  });

  it('holds the rebuilt row objects rather than the ones that were clicked', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows);

    await clickCell(wrapper, 0);

    const rebuiltRows = threeRows();

    await wrapper.setProps({ rows: rebuiltRows });

    expect(toRaw(selectedRowsOf(wrapper)[0])).toBe(rebuiltRows[0]);
  });

  it('deselects a rebuilt row that is clicked again', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows);

    await clickCell(wrapper, 0);
    await clickCell(wrapper, 1, EXTENDS_SELECTION);

    const rebuiltRows = threeRows();

    await wrapper.setProps({ rows: rebuiltRows });
    await clickCell(wrapper, 1, ADDS_TO_SELECTION);

    expect(selectedRowsOf(wrapper).map(toRaw)).toEqual([rebuiltRows[0]]);
  });

  it('extends the selection from an anchor row that was rebuilt', async() => {
    const rows = threeRows();
    const wrapper = mountTable(rows);

    await clickCell(wrapper, 0);

    const rebuiltRows = threeRows();

    await wrapper.setProps({ rows: rebuiltRows });
    await clickCell(wrapper, 2, EXTENDS_SELECTION);

    expect(selectedRowsOf(wrapper)).toEqual(rebuiltRows);
  });
});
