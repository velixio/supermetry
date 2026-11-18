import { registerCatalogue } from '@/lib/i18n'

// Strings of the getting-started checklist, the add-site install step, the empty states of the
// analytics pages and the goal and funnel value picker. Read them when rendering, never at module scope.
export const uiOnboarding = {
  checklist: {
    title: 'Get started',
    progress: '{done} of {total} done',
    listLabel: 'Getting started steps',
    done: 'Done',
    todo: 'To do',
    dismiss: 'Dismiss getting started checklist',
    allDone: 'You are all set',
    sampleData: 'Explore with sample data',
    items: {
      snippet: { title: 'Add the snippet', hint: 'Paste the tracking code into your site.', action: 'Open install steps' },
      firstEvent: { title: 'See your first event', hint: 'Open your site, then watch the visit arrive.', action: 'Watch live' },
      goal: { title: 'Add a goal', hint: 'Count the pages and events that matter.', action: 'Create goal' },
      invite: { title: 'Invite a teammate', hint: 'Share the dashboard with the people who need it.', action: 'Invite teammate' },
      mcp: { title: 'Connect an AI assistant via MCP', hint: 'Ask questions about your stats in your assistant.', action: 'Create API key' },
    },
  },
  install: {
    title: 'Install the tracker',
    description: 'Pick your stack and paste the snippet. We will tell you when the first event arrives.',
    tabsLabel: 'Installation method',
    siteIdLabel: 'Site ID',
    whereLabel: 'Where',
    copySnippet: 'Copy snippet',
    copyCommand: 'Copy install command',
    copied: 'Copied',
    copyManual: 'Select the text and press Ctrl+C to copy',
    waiting: 'Waiting for the first event…',
    waitingHint: 'Open your site in another tab. This page checks every few seconds.',
    received: 'First event received',
    receivedHint: 'Your site is sending data.',
    goToDashboard: 'Go to dashboard',
    statusLabel: 'Installation status',
    chipWaiting: 'Waiting',
    chipReceived: 'Receiving',
    loading: 'Loading snippet…',
    failed: 'The snippet could not be loaded.',
  },
  picker: {
    legend: 'Seen on your site',
    pageSuggestion: '{value}, {count} views',
    eventSuggestion: '{value}, {count} events',
    none: 'Nothing seen yet. Type a value instead.',
  },
  empty: {
    docs: 'Read the docs',
    events: { title: 'No custom events yet', purpose: 'See every event your site sends, with its properties.', action: 'Install the tracker' },
    sessions: { title: 'No sessions yet', purpose: 'Follow individual visits from landing to exit.', action: 'Install the tracker' },
    profiles: { title: 'No profiles yet', purpose: 'See what each known user did across visits.', action: 'Set up identify' },
    cohorts: { title: 'No cohorts yet', purpose: 'Group visitors by what they did, then compare them.', action: 'Create cohort' },
    funnels: { title: 'No funnels yet', purpose: 'See where visitors drop off between steps.', action: 'Create funnel' },
    goals: { title: 'No goals yet', purpose: 'Count the pages and events that matter to you.', action: 'Create goal' },
    journeys: { title: 'No journeys yet', purpose: 'See the paths visitors take through your site.', action: 'Install the tracker' },
    retention: { title: 'No retention data yet', purpose: 'See how many visitors come back, week after week.', action: 'Install the tracker' },
    heatmaps: { title: 'No heatmaps yet', purpose: 'See where visitors click and how far they scroll.', action: 'Turn on heatmaps' },
    replays: { title: 'No replays yet', purpose: 'Watch real visits to understand what visitors did.', action: 'Turn on replays' },
    revenue: { title: 'No revenue yet', purpose: 'Tie purchases to the channels that brought them.', action: 'Set up revenue' },
    vitals: { title: 'No Web Vitals yet', purpose: 'See how fast your pages feel to real visitors.', action: 'Turn on Web Vitals' },
    links: { title: 'No links yet', purpose: 'Create short links and tracking pixels, and count every view.', action: 'Create link' },
  },
}

registerCatalogue('uiOnboarding', uiOnboarding)
