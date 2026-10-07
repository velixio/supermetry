import { Skeleton } from '@/components/ui/skeleton'

export function RoutePending() {
  return (
    <section
      aria-busy="true"
      aria-label="Loading page"
      className="flex w-full flex-col gap-6"
      role="status"
    >
      <div className="flex flex-col gap-2">
        <Skeleton className="h-7 w-48" />
        <Skeleton className="h-4 w-full max-w-md" />
      </div>
      <div className="grid gap-4 md:grid-cols-3">
        <Skeleton className="h-24" />
        <Skeleton className="h-24" />
        <Skeleton className="h-24" />
      </div>
      <Skeleton className="h-72 w-full" />
    </section>
  )
}
