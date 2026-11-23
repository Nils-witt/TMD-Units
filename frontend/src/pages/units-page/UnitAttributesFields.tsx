import AddIcon from '@mui/icons-material/Add';
import DeleteIcon from '@mui/icons-material/DeleteOutlined';
import {
  Button,
  FormControlLabel,
  IconButton,
  MenuItem,
  Stack,
  Switch,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import type { UnitAttributeType } from '../../api/types';
import {
  type AttributeDraft,
  type AttributeDraftErrors,
  MAX_ATTRIBUTE_KEY_LENGTH,
  MAX_ATTRIBUTE_TEXT_LENGTH,
  MAX_ATTRIBUTE_URL_LENGTH,
  MAX_ATTRIBUTES,
  newAttributeDraft,
  unitAttributeTypes,
} from '../../lib/unitAttributes';
import './UnitAttributesFields.scss';

/** Edits a unit's free attributes: one row of key, type and value each. */
export default function UnitAttributesFields({
  value,
  errors,
  onChange,
}: {
  value: AttributeDraft[];
  errors: AttributeDraftErrors[];
  onChange: (value: AttributeDraft[]) => void;
}) {
  const { t } = useTranslation();
  const update = (id: number, change: Partial<AttributeDraft>) =>
    onChange(value.map((d) => (d.id === id ? { ...d, ...change } : d)));

  return (
    <Stack spacing={1.5}>
      {value.length === 0 && (
        <Typography variant="body2" color="text.secondary">
          {t('unitAttributes.none')}
        </Typography>
      )}
      {value.map((d, i) => {
        const err = errors[i] ?? {};
        return (
          <Stack
            key={d.id}
            direction="row"
            spacing={1}
            useFlexGap
            className="unit-attributes-fields__row"
          >
            <TextField
              size="small"
              label={t('unitAttributes.key')}
              value={d.key}
              onChange={(e) => update(d.id, { key: e.target.value })}
              error={err.key != null}
              helperText={err.key && t(`unitAttributes.errors.${err.key}`)}
              slotProps={{ htmlInput: { maxLength: MAX_ATTRIBUTE_KEY_LENGTH } }}
              className="unit-attributes-fields__key"
            />
            <TextField
              select
              size="small"
              label={t('unitAttributes.type')}
              value={d.type}
              onChange={(e) => update(d.id, { type: e.target.value as UnitAttributeType })}
              className="unit-attributes-fields__type"
            >
              {unitAttributeTypes.map((type) => (
                <MenuItem key={type} value={type}>
                  {t(`unitAttributes.types.${type}`)}
                </MenuItem>
              ))}
            </TextField>
            <div className="unit-attributes-fields__value">
              <AttributeValueField
                draft={d}
                error={err.value}
                onChange={(change) => update(d.id, change)}
              />
            </div>
            <Tooltip title={t('unitAttributes.remove')}>
              <IconButton
                aria-label={t('unitAttributes.remove')}
                onClick={() => onChange(value.filter((x) => x.id !== d.id))}
                className="unit-attributes-fields__remove"
              >
                <DeleteIcon />
              </IconButton>
            </Tooltip>
          </Stack>
        );
      })}
      <div>
        <Button
          size="small"
          startIcon={<AddIcon />}
          disabled={value.length >= MAX_ATTRIBUTES}
          onClick={() => onChange([...value, newAttributeDraft()])}
        >
          {t('unitAttributes.add')}
        </Button>
      </div>
    </Stack>
  );
}

function AttributeValueField({
  draft,
  error,
  onChange,
}: {
  draft: AttributeDraft;
  error: AttributeDraftErrors['value'];
  onChange: (change: Partial<AttributeDraft>) => void;
}) {
  const { t } = useTranslation();
  if (draft.type === 'boolean') {
    return (
      <FormControlLabel
        control={
          <Switch checked={draft.flag} onChange={(e) => onChange({ flag: e.target.checked })} />
        }
        label={draft.flag ? t('unitAttributes.yes') : t('unitAttributes.no')}
      />
    );
  }
  const common = {
    size: 'small' as const,
    fullWidth: true,
    label: t('unitAttributes.value'),
    value: draft.text,
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      onChange({ text: e.target.value }),
    error: error != null,
    helperText: error && t(`unitAttributes.errors.${error}`),
  };
  switch (draft.type) {
    case 'number':
      return <TextField {...common} slotProps={{ htmlInput: { inputMode: 'decimal' } }} />;
    case 'date':
      return <TextField {...common} type="date" slotProps={{ inputLabel: { shrink: true } }} />;
    case 'url':
      return (
        <TextField
          {...common}
          type="url"
          placeholder="https://"
          slotProps={{ htmlInput: { maxLength: MAX_ATTRIBUTE_URL_LENGTH } }}
        />
      );
    default:
      return (
        <TextField
          {...common}
          multiline
          maxRows={4}
          slotProps={{ htmlInput: { maxLength: MAX_ATTRIBUTE_TEXT_LENGTH } }}
        />
      );
  }
}
