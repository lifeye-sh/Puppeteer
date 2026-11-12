import {useQuery} from '@tanstack/react-query';
import requests from '@/api/core/requests';
export function useBackendBranding(){
 return useQuery({queryKey:['backend-branding'],queryFn:()=>requests.get<{name:string;copyright:string;icp:string;logo:string}>('/branding',{errorMode:'local'}),staleTime:30000,refetchInterval:30000});
}
