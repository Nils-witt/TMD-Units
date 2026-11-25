import { useApi } from './useApi';
import { queryKeys } from '../api/queryKeys';
import { type ApiQueryResult, useApiQuery } from './useApiQuery';
import type { UnitAlarm } from '../api/types';

const NO_ALARMS: UnitAlarm[] = [];

/** GET /api/units/{id}/alarms, newest first; refetched when the unit event
 * stream reports an alarm for the unit. Disabled while `id` is null. */
export function useUnitAlarms(id: string | null): ApiQueryResult<UnitAlarm[]> {
  const api = useApi();
  return useApiQuery(
    {
      queryKey: queryKeys.unitAlarms(id ?? ''),
      queryFn: () => api.listUnitAlarms(id!),
      enabled: id != null,
      fresh: true,
    },
    NO_ALARMS,
  );
}
