import { useTranslation } from 'react-i18next';

interface UnsupportedFeatureProps {
  featureName?: string;
}

export default function UnsupportedFeature({ featureName }: UnsupportedFeatureProps) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center justify-center py-32 px-4 text-center">
      <div className="rounded-full bg-muted p-4 mb-4">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="32"
          height="32"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="text-muted-foreground"
        >
          <path d="M10.29 3.86 1.82 15a3 3 0 0 0 2.36 5h16.64a3 3 0 0 0 2.36-5L13.71 3.86a3 3 0 0 0-5.42 0z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
      </div>
      <h2 className="text-xl font-semibold mb-2">
        {t('common.unsupportedFeature', 'This feature is not yet supported in gonacos')}
      </h2>
      {featureName && (
        <p className="text-muted-foreground text-sm">{featureName}</p>
      )}
      <p className="text-muted-foreground text-sm mt-2 max-w-md">
        {t('common.unsupportedFeatureHint', 'The gonacos backend is still implementing this capability. Please use the legacy console or the REST API directly.')}
      </p>
    </div>
  );
}
