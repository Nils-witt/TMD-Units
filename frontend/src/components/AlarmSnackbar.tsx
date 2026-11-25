import { useEffect, useState } from 'react';
import { Alert, AlertTitle, Snackbar } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { queryKeys } from '../api/queryKeys';
import type { Unit, UnitAlarm } from '../api/types';
import { onAlarm } from '../lib/alarmNotifications';

/** Announces alarms pushed over the unit event stream while a page that
 * follows it (units list, map) is open. Shows the latest one until closed. */
export default function AlarmSnackbar() {
  const { t } = useTranslation();
  const [alarm, setAlarm] = useState<UnitAlarm | null>(null);
  const queryClient = useQueryClient();
  // Only follows the stream the open page opened; it doesn't open its own.
  useEffect(() => onAlarm(setAlarm), []);

  const units = queryClient.getQueryData<Unit[]>(queryKeys.units) ?? [];
  const unitName = units.find((u) => u.id === alarm?.unitId)?.name ?? '';
  return (
    <Snackbar
      open={alarm !== null}
      anchorOrigin={{ vertical: 'top', horizontal: 'center' }}
      onClose={(_, reason) => reason !== 'clickaway' && setAlarm(null)}
    >
      <Alert severity="error" variant="filled" onClose={() => setAlarm(null)}>
        <AlertTitle>{t('unitAlarms.notification', { name: unitName })}</AlertTitle>
        {alarm?.title}
        {alarm?.address && <div>{alarm.address}</div>}
      </Alert>
    </Snackbar>
  );
}
