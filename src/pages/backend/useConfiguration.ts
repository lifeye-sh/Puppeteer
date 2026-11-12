import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';
import type {Configuration,Definition} from './configuration-model';
export function useConfiguration(enabled=true){
 return useQuery({queryKey:['configuration-current'],enabled,queryFn:()=>requests.get<Configuration>('/settings',{errorMode:'local'}),refetchInterval:5000});
}
export function useConfigurationSchema(enabled=true){
 return useQuery({queryKey:['configuration-schema'],enabled,queryFn:()=>requests.get<{properties:Definition[];readOnlyProperties?:Definition[]}>('/settings/schema',{errorMode:'local'}),staleTime:60000});
}
