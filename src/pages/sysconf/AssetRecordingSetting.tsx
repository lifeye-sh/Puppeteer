import {Alert, Button, Collapse, Form, Input, InputNumber, Radio, Select, Space, Switch} from "antd";
import {useTranslation} from "react-i18next";
import {useFormRequest} from "@/hook/use-antd-form-query";
import {SettingProps} from "./SettingPage";
import {useState} from "react";

const isTruthy = (value: any) => value === true || `${value}`.toLowerCase() === 'true';
const savedSecretPlaceholder = '******';
const hasTextValue = (value: any) => typeof value === 'string' ? value.trim() !== '' : !!value;
const bytesPerKiB = 1024;
const bytesPerMiB = 1024 * bytesPerKiB;
const defaultS3PartSize = 16;
const defaultS3MultipartThreshold = 64;
const defaultS3Concurrency = 3;
const minimumS3PartSizeBytes = 5 * bytesPerMiB;
const maximumS3PartSizeBytes = 5 * 1024 * bytesPerMiB;
const maximumS3Concurrency = 100;
const defaultDataSizeUnit: DataSizeUnit = 'mib';

type DataSizeUnit = 'bytes' | 'kib' | 'mib';

interface DataSizeValue {
    size?: number;
    unit?: DataSizeUnit;
}

interface DataSizeInputProps {
    id?: string;
    value?: DataSizeValue;
    onChange?: (value: DataSizeValue) => void;
    disabled?: boolean;
    minBytes?: number;
    maxBytes?: number;
    unitOptions: {label: string; value: DataSizeUnit}[];
}

const dataSizeUnitFactors: Record<DataSizeUnit, number> = {
    bytes: 1,
    kib: bytesPerKiB,
    mib: bytesPerMiB
};

const normalizeDataSizeUnit = (value: any): DataSizeUnit => {
    const unit = `${value}`.toLowerCase();
    return unit === 'bytes' || unit === 'kib' || unit === 'mib' ? unit : defaultDataSizeUnit;
};

const dataSizeToBytes = (value?: DataSizeValue) => {
    if (!value || typeof value.size !== 'number' || !Number.isFinite(value.size) || value.size <= 0) {
        return undefined;
    }
    const factor = dataSizeUnitFactors[normalizeDataSizeUnit(value.unit)];
    const bytes = Number(value.size) * factor;
    return Number.isSafeInteger(bytes) ? bytes : undefined;
};

const DataSizeInput = ({id, value = {}, onChange, disabled, minBytes, maxBytes, unitOptions}: DataSizeInputProps) => {
    const unit = normalizeDataSizeUnit(value.unit);
    const factor = dataSizeUnitFactors[unit];
    const min = minBytes === undefined ? 1 : Math.ceil(minBytes / factor);
    const maxBytesLimit = maxBytes === undefined ? Number.MAX_SAFE_INTEGER : maxBytes;
    const max = Math.floor(maxBytesLimit / factor);

    return <Space.Compact>
        <InputNumber
            id={id}
            disabled={disabled}
            min={min}
            max={max}
            value={value.size}
            onChange={(size) => onChange?.({size: size ?? undefined, unit})}
            style={{width: 180}}
        />
        <Select
            disabled={disabled}
            value={unit}
            options={unitOptions}
            onChange={(nextUnit: DataSizeUnit) => {
                const bytes = dataSizeToBytes({size: value.size, unit});
                const nextSize = bytes === undefined
                    ? value.size
                    : bytes / dataSizeUnitFactors[nextUnit];
                onChange?.({size: nextSize, unit: nextUnit});
            }}
            style={{width: 100}}
        />
    </Space.Compact>;
};

const toPositiveInteger = (value: any, fallback: number) => {
    const parsed = Number(value);
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback;
};

const toPositiveNumber = (value: any, fallback: number) => {
    const parsed = Number(value);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
};

const AssetRecordingSetting = ({
                                   get,
                                   set
                               }: SettingProps) => {
    const {t} = useTranslation();
    const [form] = Form.useForm();
    const recordingEnabled = Form.useWatch('recording-enabled', form);
    const recordingType = Form.useWatch('recording-type', form);
    const [s3SecretAccessKeyExists, setS3SecretAccessKeyExists] = useState(false);
    const [webDAVPasswordExists, setWebDAVPasswordExists] = useState(false);
    const isRecordingEnabled = isTruthy(recordingEnabled);
    const dataSizeUnitOptions: DataSizeInputProps['unitOptions'] = [{
        label: t('settings.asset_access.recording_s3_unit_bytes'),
        value: 'bytes'
    }, {
        label: 'KiB',
        value: 'kib'
    }, {
        label: 'MiB',
        value: 'mib'
    }];

    const wrapGet = async () => {
        const values = {...await get()};
        setS3SecretAccessKeyExists(isTruthy(values['recording-s3-secret-access-key-exists']));
        setWebDAVPasswordExists(isTruthy(values['recording-webdav-password-exists']));
        values['recording-enabled'] = values['recording-enabled'] === undefined
            ? true
            : isTruthy(values['recording-enabled']);
        values['recording-type'] = values['recording-type'] || 'local';
        values['recording-s3-use-ssl'] = values['recording-s3-use-ssl'] === undefined || values['recording-s3-use-ssl'] === ''
            ? true
            : isTruthy(values['recording-s3-use-ssl']);
        values['recording-s3-path-style'] = values['recording-s3-path-style'] === undefined || values['recording-s3-path-style'] === ''
            ? false
            : isTruthy(values['recording-s3-path-style']);
        values['recording-s3-force-payload-signing'] = values['recording-s3-force-payload-signing'] === undefined || values['recording-s3-force-payload-signing'] === ''
            ? false
            : isTruthy(values['recording-s3-force-payload-signing']);
        values['recording-s3-part-size-setting'] = {
            size: toPositiveNumber(values['recording-s3-part-size'], defaultS3PartSize),
            unit: normalizeDataSizeUnit(values['recording-s3-part-size-unit'])
        };
        values['recording-s3-multipart-threshold-setting'] = {
            size: toPositiveNumber(values['recording-s3-multipart-threshold'], defaultS3MultipartThreshold),
            unit: normalizeDataSizeUnit(values['recording-s3-multipart-threshold-unit'])
        };
        values['recording-s3-concurrency'] = toPositiveInteger(
            values['recording-s3-concurrency'],
            defaultS3Concurrency
        );
        values['recording-s3-secret-access-key'] = '';
        values['recording-webdav-password'] = '';
        return values;
    };

    const wrapSet = (formValues: any) => {
        const values = {...formValues};
        values['recording-enabled'] = values['recording-enabled'] === undefined
            ? false
            : isTruthy(values['recording-enabled']);
        values['recording-type'] = values['recording-type'] || 'local';

        const partSize = values['recording-s3-part-size-setting'] as DataSizeValue | undefined;
        const multipartThreshold = values['recording-s3-multipart-threshold-setting'] as DataSizeValue | undefined;
        delete values['recording-s3-part-size-setting'];
        delete values['recording-s3-multipart-threshold-setting'];

        if (values['recording-type'] !== 's3') {
            delete values['recording-s3-endpoint'];
            delete values['recording-s3-region'];
            delete values['recording-s3-access-key-id'];
            delete values['recording-s3-secret-access-key'];
            delete values['recording-s3-bucket'];
            delete values['recording-s3-use-ssl'];
            delete values['recording-s3-path-style'];
            delete values['recording-s3-force-payload-signing'];
            delete values['recording-s3-part-size'];
            delete values['recording-s3-part-size-unit'];
            delete values['recording-s3-multipart-threshold'];
            delete values['recording-s3-multipart-threshold-unit'];
            delete values['recording-s3-concurrency'];
        } else {
            values['recording-s3-part-size'] = partSize?.size;
            values['recording-s3-part-size-unit'] = normalizeDataSizeUnit(partSize?.unit);
            values['recording-s3-multipart-threshold'] = multipartThreshold?.size;
            values['recording-s3-multipart-threshold-unit'] = normalizeDataSizeUnit(multipartThreshold?.unit);
            if (!hasTextValue(values['recording-s3-secret-access-key'])) {
                delete values['recording-s3-secret-access-key'];
            }
        }

        if (values['recording-type'] !== 'webdav') {
            delete values['recording-webdav-endpoint'];
            delete values['recording-webdav-username'];
            delete values['recording-webdav-password'];
            delete values['recording-webdav-directory'];
        } else if (!hasTextValue(values['recording-webdav-password'])) {
            delete values['recording-webdav-password'];
        }

        delete values['recording-path'];
        delete values['recording-s3-secret-access-key-exists'];
        delete values['recording-webdav-password-exists'];

        return set(values);
    };

    useFormRequest(form, ["form-request", "web/src/pages/sysconf/AssetRecordingSetting.tsx"], wrapGet, true);

    return <Form form={form} onFinish={wrapSet} layout="vertical">
        <Form.Item
            name="recording-enabled"
            label={t('identity.user.recording')}
            required={true}
            valuePropName="checked"
            extra={t('settings.asset_access.recording_tip')}
        >
            <Switch checkedChildren={t('general.enabled')} unCheckedChildren={t('general.disabled')}/>
        </Form.Item>

        <div className="mb-4">
            <Alert
                type="info"
                showIcon
                title={t('settings.asset_access.recording_storage_tip')}
            />
        </div>

        <Form.Item name="recording-type" label={t('settings.asset_access.recording_storage_type')} required={true}>
            <Radio.Group disabled={!isRecordingEnabled} options={[{
                label: t('settings.asset_access.recording_storage_local'),
                value: 'local'
            }, {
                label: 'S3',
                value: 's3'
            }, {
                label: 'WebDAV',
                value: 'webdav'
            }]}/>
        </Form.Item>

        {recordingType === 'local' && <Form.Item
            name="recording-path"
            label={t('settings.asset_access.recording_path')}
            extra={t('settings.asset_access.recording_path_readonly_tip')}
        >
            <Input disabled placeholder="/usr/local/puppeteer/data/recording" style={{maxWidth: 520}}/>
        </Form.Item>}

        {recordingType === 's3' && <>
            <Form.Item
                name="recording-s3-endpoint"
                label={t('settings.asset_access.recording_s3_endpoint')}
                required={isRecordingEnabled}
                rules={[{
                    required: isRecordingEnabled,
                    message: t('settings.asset_access.recording_s3_endpoint_required')
                }]}
            >
                <Input disabled={!isRecordingEnabled} placeholder="s3.amazonaws.com" style={{maxWidth: 520}}/>
            </Form.Item>
            <Form.Item
                name="recording-s3-region"
                label={t('settings.asset_access.recording_s3_region')}
                extra={t('settings.asset_access.recording_s3_region_tip')}
            >
                <Input disabled={!isRecordingEnabled} placeholder="us-east-1" style={{maxWidth: 520}}/>
            </Form.Item>
            <Form.Item
                name="recording-s3-access-key-id"
                label={t('settings.asset_access.recording_s3_access_key_id')}
                required={isRecordingEnabled}
                rules={[{
                    required: isRecordingEnabled,
                    message: t('settings.asset_access.recording_s3_access_key_id_required')
                }]}
            >
                <Input disabled={!isRecordingEnabled} style={{maxWidth: 520}}/>
            </Form.Item>
            <Form.Item
                name="recording-s3-secret-access-key"
                label={t('settings.asset_access.recording_s3_secret_access_key')}
                required={isRecordingEnabled && !s3SecretAccessKeyExists}
                rules={[{
                    required: isRecordingEnabled && !s3SecretAccessKeyExists,
                    message: t('settings.asset_access.recording_s3_secret_access_key_required')
                }]}
            >
                <Input.Password
                    disabled={!isRecordingEnabled}
                    placeholder={s3SecretAccessKeyExists ? savedSecretPlaceholder : ''}
                    style={{maxWidth: 520}}
                />
            </Form.Item>
            <Form.Item
                name="recording-s3-bucket"
                label={t('settings.asset_access.recording_s3_bucket')}
                required={isRecordingEnabled}
                rules={[{
                    required: isRecordingEnabled,
                    message: t('settings.asset_access.recording_s3_bucket_required')
                }]}
            >
                <Input disabled={!isRecordingEnabled} placeholder="recordings" style={{maxWidth: 520}}/>
            </Form.Item>
            <Collapse
                style={{maxWidth: 740, marginBottom: 24}}
                items={[{
                    key: 's3-multipart-settings',
                    label: t('settings.asset_access.recording_s3_advanced_settings'),
                    forceRender: true,
                    children: <div className="flex flex-wrap gap-4">
                        <Form.Item
                            name="recording-s3-part-size-setting"
                            label={t('settings.asset_access.recording_s3_part_size')}
                            extra={t('settings.asset_access.recording_s3_part_size_tip')}
                            required={isRecordingEnabled}
                            rules={[{
                                validator(_, value: DataSizeValue | undefined) {
                                    if (!isRecordingEnabled) {
                                        return Promise.resolve();
                                    }
                                    if (!value || typeof value.size !== 'number' || value.size <= 0) {
                                        return Promise.reject(new Error(t('settings.asset_access.recording_s3_part_size_required')));
                                    }
                                    const bytes = dataSizeToBytes(value);
                                    if (bytes === undefined) {
                                        return Promise.reject(new Error(t('settings.asset_access.recording_s3_size_invalid')));
                                    }
                                    if (bytes < minimumS3PartSizeBytes || bytes > maximumS3PartSizeBytes) {
                                        return Promise.reject(new Error(t('settings.asset_access.recording_s3_part_size_range')));
                                    }
                                    return Promise.resolve();
                                }
                            }]}
                        >
                            <DataSizeInput
                                disabled={!isRecordingEnabled}
                                minBytes={minimumS3PartSizeBytes}
                                maxBytes={maximumS3PartSizeBytes}
                                unitOptions={dataSizeUnitOptions}
                            />
                        </Form.Item>
                        <Form.Item
                            name="recording-s3-multipart-threshold-setting"
                            label={t('settings.asset_access.recording_s3_multipart_threshold')}
                            extra={t('settings.asset_access.recording_s3_multipart_threshold_tip')}
                            required={isRecordingEnabled}
                            dependencies={['recording-s3-part-size-setting']}
                            rules={[{
                                validator(_, value: DataSizeValue | undefined) {
                                    if (!isRecordingEnabled) {
                                        return Promise.resolve();
                                    }
                                    if (!value || typeof value.size !== 'number' || value.size <= 0) {
                                        return Promise.reject(new Error(t('settings.asset_access.recording_s3_multipart_threshold_required')));
                                    }
                                    const bytes = dataSizeToBytes(value);
                                    if (bytes === undefined) {
                                        return Promise.reject(new Error(t('settings.asset_access.recording_s3_size_invalid')));
                                    }
                                    const partSizeBytes = dataSizeToBytes(form.getFieldValue('recording-s3-part-size-setting'));
                                    if (partSizeBytes !== undefined && bytes >= partSizeBytes) {
                                        return Promise.resolve();
                                    }
                                    return Promise.reject(new Error(t('settings.asset_access.recording_s3_multipart_threshold_range')));
                                }
                            }]}
                        >
                            <DataSizeInput
                                disabled={!isRecordingEnabled}
                                minBytes={minimumS3PartSizeBytes}
                                unitOptions={dataSizeUnitOptions}
                            />
                        </Form.Item>
                        <Form.Item
                            name="recording-s3-concurrency"
                            label={t('settings.asset_access.recording_s3_concurrency')}
                            extra={t('settings.asset_access.recording_s3_concurrency_tip')}
                            required={isRecordingEnabled}
                            rules={[{
                                required: isRecordingEnabled,
                                message: t('settings.asset_access.recording_s3_concurrency_required')
                            }, {
                                type: 'number',
                                min: 1,
                                max: maximumS3Concurrency,
                                message: t('settings.asset_access.recording_s3_concurrency_range')
                            }]}
                        >
                            <InputNumber
                                disabled={!isRecordingEnabled}
                                min={1}
                                max={maximumS3Concurrency}
                                precision={0}
                                style={{width: 220}}
                            />
                        </Form.Item>
                        <Form.Item
                            name="recording-s3-force-payload-signing"
                            label={t('settings.asset_access.recording_s3_force_payload_signing')}
                            extra={t('settings.asset_access.recording_s3_force_payload_signing_tip')}
                            valuePropName="checked"
                        >
                            <Switch
                                disabled={!isRecordingEnabled}
                                checkedChildren={t('general.enabled')}
                                unCheckedChildren={t('general.disabled')}
                            />
                        </Form.Item>
                    </div>
                }]}
            />
            <div className="flex flex-wrap gap-4">
                <Form.Item name="recording-s3-use-ssl" label={t('settings.asset_access.recording_s3_use_ssl')}
                           valuePropName="checked">
                    <Switch disabled={!isRecordingEnabled} checkedChildren={t('general.enabled')} unCheckedChildren={t('general.disabled')}/>
                </Form.Item>
                <Form.Item name="recording-s3-path-style" label={t('settings.asset_access.recording_s3_path_style')}
                           valuePropName="checked">
                    <Switch disabled={!isRecordingEnabled} checkedChildren={t('general.enabled')} unCheckedChildren={t('general.disabled')}/>
                </Form.Item>
            </div>
        </>}

        {recordingType === 'webdav' && <>
            <Form.Item
                name="recording-webdav-endpoint"
                label={t('settings.asset_access.recording_webdav_endpoint')}
                required={isRecordingEnabled}
                rules={[{
                    required: isRecordingEnabled,
                    message: t('settings.asset_access.recording_webdav_endpoint_required')
                }]}
            >
                <Input disabled={!isRecordingEnabled} placeholder="https://example.com/dav" style={{maxWidth: 520}}/>
            </Form.Item>
            <Form.Item
                name="recording-webdav-username"
                label={t('settings.asset_access.recording_webdav_username')}
                required={isRecordingEnabled}
                rules={[{
                    required: isRecordingEnabled,
                    message: t('settings.asset_access.recording_webdav_username_required')
                }]}
            >
                <Input disabled={!isRecordingEnabled} style={{maxWidth: 520}}/>
            </Form.Item>
            <Form.Item
                name="recording-webdav-password"
                label={t('settings.asset_access.recording_webdav_password')}
            >
                <Input.Password
                    disabled={!isRecordingEnabled}
                    placeholder={webDAVPasswordExists ? savedSecretPlaceholder : ''}
                    style={{maxWidth: 520}}
                />
            </Form.Item>
            <Form.Item
                name="recording-webdav-directory"
                label={t('settings.asset_access.recording_webdav_directory')}
            >
                <Input disabled={!isRecordingEnabled} placeholder="/recordings" style={{maxWidth: 520}}/>
            </Form.Item>
        </>}

        <Form.Item>
            <Button type="primary" htmlType="submit">{t("actions.save")}</Button>
        </Form.Item>
    </Form>;
};

export default AssetRecordingSetting;
