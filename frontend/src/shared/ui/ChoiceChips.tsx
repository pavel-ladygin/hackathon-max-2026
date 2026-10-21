import { Chip, ChipGroup } from './index'

export function ChoiceChips<T extends string>({ options, selected, onToggle, className = '', formatLabel }: { options: Array<[T, string]>; selected: T[]; onToggle: (value: T) => void; className?: string; formatLabel?: (value: T, label: string) => string }) {
  return <ChipGroup className={className}>{options.map(([value, label]) => <Chip key={value} selected={selected.includes(value)} aria-label={formatLabel ? label : undefined} onClick={() => onToggle(value)}>{formatLabel ? formatLabel(value, label) : label}</Chip>)}</ChipGroup>
}
