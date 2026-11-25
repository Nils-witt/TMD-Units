import {
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import type { Unit } from '../../api/types';
import ErrorBanner from '../../components/ErrorBanner';
import Modal from '../../components/Modal';
import { useUnitAlarms } from '../../hooks/useUnitAlarms';
import { fmtDate } from '../../lib/format';
import './UnitAlarmsDialog.scss';

/** Mounted by the modal only while it is open, so it fetches fresh each time. */
function AlarmsTable({ unit }: { unit: Unit }) {
  const { t, i18n } = useTranslation();
  const { data: alarms, loading, error } = useUnitAlarms(unit.id);
  return (
    <>
      <ErrorBanner message={error} />
      <TableContainer>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>{t('unitAlarms.raised')}</TableCell>
              <TableCell>{t('unitAlarms.alarm')}</TableCell>
              <TableCell>{t('unitAlarms.address')}</TableCell>
              <TableCell>{t('unitsListCard.position')}</TableCell>
              <TableCell>{t('unitAlarms.reference')}</TableCell>
              <TableCell>{t('unitAlarms.received')}</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {alarms.map((a) => (
              <TableRow key={a.id} hover className="unit-alarms__row">
                <TableCell>{fmtDate(a.timestamp, i18n.language)}</TableCell>
                <TableCell>
                  <strong>{a.title}</strong>
                  {a.details && (
                    <Typography variant="body2" className="unit-alarms__details">
                      {a.details}
                    </Typography>
                  )}
                </TableCell>
                <TableCell>{a.address ?? '–'}</TableCell>
                <TableCell>
                  {a.lat.toFixed(6)}, {a.lon.toFixed(6)}
                </TableCell>
                <TableCell>{a.referenceId ?? '–'}</TableCell>
                <TableCell>
                  {fmtDate(a.updatedAt, i18n.language)}
                  <Typography variant="caption" color="text.secondary" component="div">
                    {t('unitRow.by', {
                      name: a.updatedBy?.username ?? t('unitRow.deletedUser'),
                    })}
                  </Typography>
                </TableCell>
              </TableRow>
            ))}
            {alarms.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} align="center">
                  {loading ? t('common.loading') : t('unitAlarms.empty')}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </TableContainer>
    </>
  );
}

/** Shows the alarms sent to a unit, newest first. */
export default function UnitAlarmsDialog({
  open,
  unit,
  onClose,
}: {
  open: boolean;
  unit: Unit | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Modal open={open} title={t('unitAlarms.title', { name: unit?.name ?? '' })} onClose={onClose}>
      {unit && <AlarmsTable unit={unit} />}
    </Modal>
  );
}
