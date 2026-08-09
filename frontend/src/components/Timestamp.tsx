type TimestampProps = {
  value: string;
  className: string;
  compact?: boolean;
};

const compactFormatter = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

const messageFormatter = new Intl.DateTimeFormat(undefined, {
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

const fullFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "full",
  timeStyle: "long",
});

export function Timestamp({ value, className, compact = false }: TimestampProps) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return (
    <time className={className} dateTime={date.toISOString()} title={fullFormatter.format(date)}>
      {(compact ? compactFormatter : messageFormatter).format(date)}
    </time>
  );
}
