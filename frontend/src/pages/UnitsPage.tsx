import { useMemo, useRef, useState, type ChangeEvent } from 'react';
import { Alert, Box, Button, TextField } from '@mui/material';
import AddIcon from '@mui/icons-material/Add';
import FileDownloadIcon from '@mui/icons-material/FileDownload';
import FileUploadIcon from '@mui/icons-material/FileUpload';
import { useTranslation } from 'react-i18next';
import type { Unit, UnitImportResult } from '../api/types';
import ErrorBanner from '../components/ErrorBanner';
import { useApi } from '../hooks/useApi';
import { useConfirm } from '../hooks/useConfirm';
import { useUnits } from '../hooks/useUnits';
import { errorMessage } from '../lib/errors';
import type { SortDirection } from '../lib/sort';
import UnitDialog, { type UnitSubmission } from './units-page/UnitDialog';
import UnitHistoryDialog from './units-page/UnitHistoryDialog';
import UnitsListCard from './units-page/UnitsListCard';
import { filterUnits, sortUnits, type UnitSortKey } from './units-page/unitSort';
import './units-page/UnitsToolbar.scss';

export default function UnitsPage() {
  const { t } = useTranslation();
  const api = useApi();
  const confirm = useConfirm();
  const { units, loading, error: loadError, reloadUnits } = useUnits();

  const [search, setSearch] = useState('');
  const [sortKey, setSortKey] = useState<UnitSortKey>('name');
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc');
  // The edited unit stays set while the dialog closes, so its form doesn't
  // flip to "new unit" during the exit animation.
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingUnit, setEditingUnit] = useState<Unit | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyUnit, setHistoryUnit] = useState<Unit | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState<UnitImportResult | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const visibleUnits = useMemo(
    () => sortUnits(filterUnits(units, search), sortKey, sortDirection),
    [units, search, sortKey, sortDirection],
  );

  const onSort = (key: UnitSortKey) => {
    if (key === sortKey) {
      setSortDirection((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      // Newest first is the useful default for dates.
      setSortDirection(key === 'updatedAt' ? 'desc' : 'asc');
    }
  };

  const openDialog = (unit: Unit | null) => {
    setEditingUnit(unit);
    setDialogOpen(true);
  };

  const openHistory = (unit: Unit) => {
    setHistoryUnit(unit);
    setHistoryOpen(true);
  };

  const onSubmit = async ({ unit, position }: UnitSubmission) => {
    const saved = editingUnit
      ? await api.updateUnit(editingUnit.id, unit)
      : await api.createUnit(unit);
    try {
      if (position === null) {
        await api.clearUnitPosition(saved.id);
      } else if (position !== undefined) {
        await api.setUnitPosition(saved.id, position);
      }
    } finally {
      // The unit itself is saved even if its position failed.
      await reloadUnits();
    }
  };

  const onDelete = async (u: Unit) => {
    const ok = await confirm({
      title: t('units.deleteUnitTitle'),
      message: t('units.deleteUnitMessage', { name: u.name }),
      confirmLabel: t('common.delete'),
      destructive: true,
    });
    if (!ok) return;
    setError(null);
    try {
      await api.deleteUnit(u.id);
      await reloadUnits();
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const onExport = async () => {
    setError(null);
    try {
      const blob = await api.exportUnitsCsv();
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = `units-${new Date().toISOString().slice(0, 10)}.csv`;
      link.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const onImport = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    // Reset so choosing the same file again fires another change.
    e.target.value = '';
    if (!file) return;
    setError(null);
    setImportResult(null);
    setImporting(true);
    try {
      setImportResult(await api.importUnitsCsv(file));
      await reloadUnits();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setImporting(false);
    }
  };

  return (
    <Box className="units-page">
      <div className="units-toolbar">
        <TextField
          label={t('units.searchLabel')}
          size="small"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <div className="units-toolbar__actions">
          <Button startIcon={<FileDownloadIcon />} onClick={() => void onExport()}>
            {t('units.exportCsv')}
          </Button>
          <Button
            startIcon={<FileUploadIcon />}
            disabled={importing}
            onClick={() => fileInput.current?.click()}
          >
            {importing ? t('units.importing') : t('units.importCsv')}
          </Button>
          <input
            ref={fileInput}
            type="file"
            accept=".csv,text/csv"
            hidden
            onChange={(e) => void onImport(e)}
          />
          <Button variant="contained" startIcon={<AddIcon />} onClick={() => openDialog(null)}>
            {t('units.newUnit')}
          </Button>
        </div>
      </div>
      <ErrorBanner message={error ?? loadError} />
      {importResult && (
        <Alert
          severity="success"
          className="units-toolbar__notice"
          onClose={() => setImportResult(null)}
        >
          {t('units.importResult', { ...importResult })}
        </Alert>
      )}
      <UnitsListCard
        units={visibleUnits}
        loading={loading}
        sortKey={sortKey}
        sortDirection={sortDirection}
        onSort={onSort}
        onEdit={openDialog}
        onHistory={openHistory}
        onDelete={(u) => void onDelete(u)}
      />
      <UnitDialog
        open={dialogOpen}
        unit={editingUnit}
        onClose={() => setDialogOpen(false)}
        onSubmit={onSubmit}
      />
      <UnitHistoryDialog
        open={historyOpen}
        unit={historyUnit}
        onClose={() => setHistoryOpen(false)}
      />
    </Box>
  );
}
